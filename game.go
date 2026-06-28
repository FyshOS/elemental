package main

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// game.go is the bridge between the board rules and the shader showcase. It owns
// one canvas.Shader per cell (positioned over a reactive background), routes
// pointer input into swaps, and runs a small state machine that animates every
// match, dissolve, drop and portal arrival from a single clock.

// Animation phases and their durations in seconds.
const (
	phaseIdle = iota
	phaseSwap     // two cells trading places
	phaseSwapBack // an illegal swap sliding back
	phaseClear    // matched cells dissolving
	phaseFall     // gravity and portal refill

	swapDur   = 0.18
	clearDur  = 0.45
	fallDur   = 0.36
	impactDur = 0.18 // how long the landing smash lingers

	maxFlows  = 16 // pooled match-beam shaders
	maxBursts = 8  // pooled compound-match explosions

	// Core-energy jeopardy. The core constantly drains and matches recharge it;
	// the drain accelerates with each level, so the player must match faster.
	energyStart   = 1.0   // starting charge, 0..1 (full)
	energyMax     = 1.0   // full charge
	baseDrain     = 0.030 // charge lost per second at level 0 (~33s idle when full)
	drainPerLevel = 0.006 // extra drain per second per level
	gainPerCell   = 0.020 // charge gained per cleared cell
	comboGain     = 0.6   // extra charge fraction per cascade depth
	burstBonus    = 0.06  // extra charge per compound-match explosion
	levelScore    = 800   // points between difficulty levels
)

type cellPos struct{ r, c int }

// Game is the playable board widget.
type Game struct {
	widget.BaseWidget

	board   grid
	shaders [boardSize][boardSize]*canvas.Shader
	bg      *canvas.Shader

	// per-cell visual state, driven each frame
	appearV [boardSize][boardSize]float64 // 0..1 portal materialise
	matchV  [boardSize][boardSize]float64 // 0..1 dissolve
	off0X   [boardSize][boardSize]float64 // start-of-phase x displacement, px
	off0Y   [boardSize][boardSize]float64 // start-of-phase y displacement, px
	fresh   [boardSize][boardSize]bool    // cells materialising this fall
	matched [boardSize][boardSize]bool    // cells dissolving this clear
	delay   [boardSize][boardSize]float64 // per-cell dissolve delay (energy travel)
	impactV [boardSize][boardSize]float64 // 0..1 landing smash, decays each frame
	fell    [boardSize][boardSize]bool    // cells that dropped this round, smash on land

	flows      [maxFlows]*canvas.Shader // pooled energy beams along matched runs
	flowActive [maxFlows]bool
	flowRuns   [maxFlows]runSpan

	bursts      [maxBursts]*canvas.Shader // pooled compound-match explosions
	burstActive [maxBursts]bool
	burstSpecs  [maxBursts]burstSpec

	// per-type compiled source, so changing a cell's element is a field swap
	srcTable [numTypes]srcEntry

	phase  int
	phaseT float64
	clock  float64
	last   time.Time

	sel      cellPos // selected cell, {-1,-1} when none
	swapA    cellPos
	swapB    cellPos
	hoverPos cellPos

	dragFrom cellPos
	dragDX   float64
	dragDY   float64

	score int
	combo int
	level int

	energy    float64 // 0..1 core charge; reaches 0 -> game over
	over      bool
	energyBar *canvas.Shader

	onScore    func(int)
	onLevel    func(int)
	onGameOver func(int)

	// ripple state for the background reaction
	rippleClock float64
	rippleX     float32
	rippleY     float32

	// cached geometry, recomputed on resize
	size fyne.Size
	cell float32
	ox   float32
	oy   float32

	anim *fyne.Animation
}

type srcEntry struct {
	name        string
	src, srcES  []byte
}

// NewGame builds a board, its shaders and the reactive background. onScore is
// called whenever the score changes.
func NewGame(onScore func(int)) *Game {
	g := &Game{onScore: onScore, sel: cellPos{-1, -1}, hoverPos: cellPos{-1, -1}}
	g.ExtendBaseWidget(g)

	for i, m := range materials {
		d := glHeaderDesktop + cellUniforms + glslHelpers + m.body + cellMain
		e := glHeaderES + cellUniforms + glslHelpers + m.body + cellMain
		g.srcTable[i] = srcEntry{"elemental_cell_" + m.name, []byte(d), []byte(e)}
	}

	g.board = newGrid()
	g.bg = newBackgroundShader()
	for i := range g.flows {
		g.flows[i] = newBeamShader()
	}
	for i := range g.bursts {
		g.bursts[i] = newBurstShader()
	}
	g.energyBar = newEnergyShader()
	g.energy = energyStart
	g.rippleClock = -100 // start with no active ripple

	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			s := newCellShader(materials[0])
			g.shaders[r][c] = s
			g.setType(r, c, g.board[r][c])
			g.appearV[r][c] = 1
		}
	}
	g.last = time.Now()
	return g
}

// setType points the slot's shader at the given element. Cells of the same
// element share a compiled program via the shared Name.
func (g *Game) setType(r, c, t int) {
	e := g.srcTable[t]
	s := g.shaders[r][c]
	s.Name = e.name
	s.Source = e.src
	s.SourceES = e.srcES
}

func smooth(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return x * x * (3 - 2*x)
}

// easeIn accelerates from rest - used for gravity so cells gather speed as they
// drop and hit hard at the bottom.
func easeIn(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return x * x * x
}

// restTopLeft returns the un-offset top-left pixel of a cell.
func (g *Game) restTopLeft(r, c int) (float32, float32) {
	return g.ox + float32(c)*g.cell, g.oy + float32(r)*g.cell
}

// cellAt maps a widget-relative pixel position to a board cell.
func (g *Game) cellAt(pos fyne.Position) (cellPos, bool) {
	if g.cell <= 0 {
		return cellPos{}, false
	}
	c := int((pos.X - g.ox) / g.cell)
	r := int((pos.Y - g.oy) / g.cell)
	if !inBounds(r, c) {
		return cellPos{}, false
	}
	return cellPos{r, c}, true
}

// --- state machine -------------------------------------------------------

func (g *Game) enterIdle() {
	g.phase = phaseIdle
	g.phaseT = 0
}

// startSwap trades two adjacent cells and animates them into place.
func (g *Game) startSwap(a, b cellPos) {
	g.swapA, g.swapB = a, b
	g.board[a.r][a.c], g.board[b.r][b.c] = g.board[b.r][b.c], g.board[a.r][a.c]
	g.setType(a.r, a.c, g.board[a.r][a.c])
	g.setType(b.r, b.c, g.board[b.r][b.c])
	g.setSwapOffsets(a, b)
	g.phase = phaseSwap
	g.phaseT = 0
}

// setSwapOffsets makes each cell start from its partner's position.
func (g *Game) setSwapOffsets(a, b cellPos) {
	g.off0X[a.r][a.c] = float64((b.c - a.c)) * float64(g.cell)
	g.off0Y[a.r][a.c] = float64((b.r - a.r)) * float64(g.cell)
	g.off0X[b.r][b.c] = float64((a.c - b.c)) * float64(g.cell)
	g.off0Y[b.r][b.c] = float64((a.r - b.r)) * float64(g.cell)
}

func (g *Game) finishSwap() {
	a, b := g.swapA, g.swapB
	g.off0X[a.r][a.c], g.off0Y[a.r][a.c] = 0, 0
	g.off0X[b.r][b.c], g.off0Y[b.r][b.c] = 0, 0

	mask, n := g.board.findMatches()
	if n == 0 {
		// Illegal move: swap the elements back and slide them home.
		g.board[a.r][a.c], g.board[b.r][b.c] = g.board[b.r][b.c], g.board[a.r][a.c]
		g.setType(a.r, a.c, g.board[a.r][a.c])
		g.setType(b.r, b.c, g.board[b.r][b.c])
		g.setSwapOffsets(a, b)
		g.phase = phaseSwapBack
		g.phaseT = 0
		return
	}
	g.combo = 0
	g.enterClear(mask, n)
}

// enterClear begins dissolving a set of matched cells and kicks off the
// background shock wave from their centre.
func (g *Game) enterClear(mask [boardSize][boardSize]bool, n int) {
	g.matched = mask

	// Award points, scaling with cascade depth.
	g.score += n * 10 * (g.combo + 1)
	if g.onScore != nil {
		g.onScore(g.score)
	}
	if lvl := g.score / levelScore; lvl != g.level {
		g.level = lvl
		if g.onLevel != nil {
			g.onLevel(lvl)
		}
	}

	// Centre of mass of the match, for the dissolve travel and the ripple.
	var sr, sc, cnt float64
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			if mask[r][c] {
				sr += float64(r)
				sc += float64(c)
				cnt++
			}
		}
	}
	if cnt > 0 {
		sr /= cnt
		sc /= cnt
	}
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			if mask[r][c] {
				// Manhattan distance from the centre, so the dissolve ripples
				// outward and energy appears to travel along the run.
				g.delay[r][c] = absf(float64(r)-sr) + absf(float64(c)-sc)
			}
		}
	}

	// Trigger the background ripple at the match centre (GL coords, y up).
	if g.size.Width > 0 && g.size.Height > 0 {
		px := g.ox + (float32(sc)+0.5)*g.cell
		py := g.oy + (float32(sr)+0.5)*g.cell
		g.rippleX = px / g.size.Width
		g.rippleY = 1 - py/g.size.Height
		g.rippleClock = g.clock
	}

	runs := g.board.findRuns()
	bursts := g.board.findBursts(runs)
	g.setupFlows(runs)
	g.setupBursts(bursts)

	// Recharge the core: more cells, deeper cascades and compound matches all
	// feed it harder.
	gain := float64(n) * gainPerCell * (1.0 + comboGain*float64(g.combo))
	gain += float64(len(bursts)) * burstBonus
	g.energy += gain
	if g.energy > energyMax {
		g.energy = energyMax
	}

	g.phase = phaseClear
	g.phaseT = 0
}

// EnergyBar returns the core-energy gauge shader for placement in the HUD.
func (g *Game) EnergyBar() *canvas.Shader { return g.energyBar }

// setupFlows assigns a pooled beam shader to each matched run, tinted for that
// run's element, so energy visibly flows between the cells as they meet.
func (g *Game) setupFlows(runs []runSpan) {
	for i := range g.flows {
		if i < len(runs) {
			g.flowRuns[i] = runs[i]
			g.flowActive[i] = true
			f := g.flows[i]
			glow := materialGlow[runs[i].typ]
			f.Uniforms["cr"] = glow[0]
			f.Uniforms["cg"] = glow[1]
			f.Uniforms["cb"] = glow[2]
			if runs[i].horiz {
				f.Uniforms["horiz"] = 1
			} else {
				f.Uniforms["horiz"] = 0
			}
			f.Show()
		} else {
			g.flowActive[i] = false
			g.flows[i].Hide()
		}
	}
}

// hideFlows stops every active beam at the end of a clear.
func (g *Game) hideFlows() {
	for i := range g.flows {
		g.flowActive[i] = false
		g.flows[i].Hide()
	}
}

// setupBursts assigns a pooled explosion to each compound-match centre.
func (g *Game) setupBursts(specs []burstSpec) {
	for i := range g.bursts {
		if i < len(specs) {
			g.burstSpecs[i] = specs[i]
			g.burstActive[i] = true
			s := g.bursts[i]
			glow := materialGlow[specs[i].typ]
			s.Uniforms["cr"] = glow[0]
			s.Uniforms["cg"] = glow[1]
			s.Uniforms["cb"] = glow[2]
			s.Show()
		} else {
			g.burstActive[i] = false
			g.bursts[i].Hide()
		}
	}
}

// hideBursts stops every active explosion at the end of a clear.
func (g *Game) hideBursts() {
	for i := range g.bursts {
		g.burstActive[i] = false
		g.bursts[i].Hide()
	}
}

// finishClear drops the survivors into the holes left by the match and tops
// each column up with fresh cells, then animates the whole cascade.
func (g *Game) finishClear() {
	moves := g.board.applyGravity(g.matched)

	// Reset per-cell animation state, then point every slot at its new element.
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			g.matchV[r][c] = 0
			g.matched[r][c] = false
			g.fresh[r][c] = false
			g.off0X[r][c], g.off0Y[r][c] = 0, 0
			g.appearV[r][c] = 1
			g.setType(r, c, g.board[r][c])
		}
	}

	// Apply the drop offsets and portal arrivals; every moved cell will smash
	// down when it lands.
	for _, m := range moves {
		g.off0Y[m.row][m.col] = float64(m.srcRow-m.row) * float64(g.cell)
		g.fell[m.row][m.col] = true
		if m.fresh {
			g.fresh[m.row][m.col] = true
			g.appearV[m.row][m.col] = 0
		}
	}

	g.hideFlows()
	g.hideBursts()
	g.phase = phaseFall
	g.phaseT = 0
}

func (g *Game) finishFall() {
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			g.off0X[r][c], g.off0Y[r][c] = 0, 0
			g.appearV[r][c] = 1
			g.fresh[r][c] = false
			if g.fell[r][c] {
				g.impactV[r][c] = 1 // kick off the landing smash
				g.fell[r][c] = false
			}
		}
	}

	mask, n := g.board.findMatches()
	if n > 0 {
		g.combo++
		g.enterClear(mask, n)
		return
	}
	if !g.board.hasMoves() {
		// reshuffle restarts the fall animation to materialise the new board, so
		// let that phase play out instead of forcing straight to idle (which
		// would leave every cell stuck invisible at appear=0).
		g.reshuffle()
		return
	}
	g.enterIdle()
}

// triggerGameOver freezes play and notifies the host once the core is dark.
func (g *Game) triggerGameOver() {
	if g.over {
		return
	}
	g.over = true
	g.sel = cellPos{-1, -1}
	g.hideFlows()
	g.hideBursts()
	if g.onGameOver != nil {
		g.onGameOver(g.score)
	}
}

// Restart resets the board and core for a fresh run.
func (g *Game) Restart() {
	for {
		g.board = newGrid()
		if g.board.hasMoves() {
			break
		}
	}
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			g.setType(r, c, g.board[r][c])
			g.appearV[r][c] = 1
			g.matchV[r][c] = 0
			g.impactV[r][c] = 0
			g.off0X[r][c], g.off0Y[r][c] = 0, 0
			g.fresh[r][c] = false
			g.fell[r][c] = false
			g.matched[r][c] = false
		}
	}
	g.hideFlows()
	g.hideBursts()
	g.energy = energyStart
	g.score = 0
	g.combo = 0
	g.level = 0
	g.over = false
	g.sel = cellPos{-1, -1}
	g.phase = phaseIdle
	g.phaseT = 0
	g.last = time.Now()
	if g.onScore != nil {
		g.onScore(0)
	}
	if g.onLevel != nil {
		g.onLevel(0)
	}
}

// reshuffle rebuilds a board that has at least one legal move.
func (g *Game) reshuffle() {
	for {
		g.board = newGrid()
		if g.board.hasMoves() {
			break
		}
	}
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			g.setType(r, c, g.board[r][c])
			g.appearV[r][c] = 0
			g.fresh[r][c] = true
			g.fell[r][c] = true
			g.off0Y[r][c] = -float64(boardSize) * float64(g.cell)
		}
	}
	g.phase = phaseFall
	g.phaseT = 0
}

// --- animation -----------------------------------------------------------

// tick advances the clock and the active phase, then refreshes every shader.
func (g *Game) tick() {
	now := time.Now()
	dt := now.Sub(g.last).Seconds()
	if dt > 0.1 {
		dt = 0.1 // clamp after a pause so nothing jumps
	}
	g.last = now
	g.clock += dt

	// The core drains constantly while playing; depletion ends the run.
	if !g.over {
		drain := baseDrain + drainPerLevel*float64(g.level)
		g.energy -= drain * dt
		if g.energy <= 0 {
			g.energy = 0
			g.triggerGameOver()
		}
	}
	if g.over {
		g.updateVisuals() // keep the shaders breathing, but freeze the game
		return
	}

	g.phaseT += dt

	switch g.phase {
	case phaseSwap:
		if g.phaseT >= swapDur {
			g.finishSwap()
		}
	case phaseSwapBack:
		if g.phaseT >= swapDur {
			a, b := g.swapA, g.swapB
			g.off0X[a.r][a.c], g.off0Y[a.r][a.c] = 0, 0
			g.off0X[b.r][b.c], g.off0Y[b.r][b.c] = 0, 0
			g.enterIdle()
		}
	case phaseClear:
		if g.phaseT >= clearDur {
			g.finishClear()
		}
	case phaseFall:
		if g.phaseT >= fallDur {
			g.finishFall()
		}
	}

	// The landing smash decays independently of the phase.
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			if g.impactV[r][c] > 0 {
				g.impactV[r][c] -= dt / impactDur
				if g.impactV[r][c] < 0 {
					g.impactV[r][c] = 0
				}
			}
		}
	}

	g.updateVisuals()
}

// updateVisuals writes per-cell uniforms and positions for the current frame.
func (g *Game) updateVisuals() {
	// Background reaction.
	g.bg.Uniforms["time"] = float32(g.clock)
	g.bg.Uniforms["rippleTime"] = float32(g.clock - g.rippleClock)
	g.bg.Uniforms["rippleX"] = g.rippleX
	g.bg.Uniforms["rippleY"] = g.rippleY
	g.bg.Uniforms["combo"] = float32(g.combo)
	danger := smooth((0.30 - g.energy) / 0.30) // ramps up as the core nears empty
	g.bg.Uniforms["danger"] = float32(danger)
	g.bg.Refresh()

	// Core-energy gauge.
	if g.energyBar != nil {
		g.energyBar.Uniforms["time"] = float32(g.clock)
		g.energyBar.Uniforms["level"] = float32(g.energy)
		g.energyBar.Refresh()
	}

	swapE := smooth(g.phaseT / swapDur)
	fallMoveE := easeIn(g.phaseT / fallDur)   // gravity accelerates into the smash
	fallAppearE := smooth(g.phaseT / fallDur) // portals fade in smoothly

	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			s := g.shaders[r][c]

			// Dissolve progress for matched cells, travelling from the centre.
			if g.phase == phaseClear && g.matched[r][c] {
				p := (g.phaseT - g.delay[r][c]*0.04) / (clearDur * 0.7)
				g.matchV[r][c] = clamp01(p)
			}

			// Portal materialise for fresh cells.
			if g.phase == phaseFall && g.fresh[r][c] {
				g.appearV[r][c] = fallAppearE
			}

			// Current offset eases from the phase's start displacement to zero.
			var ox, oy float64
			switch g.phase {
			case phaseSwap, phaseSwapBack:
				ox = g.off0X[r][c] * (1 - swapE)
				oy = g.off0Y[r][c] * (1 - swapE)
			case phaseFall:
				ox = g.off0X[r][c] * (1 - fallMoveE)
				oy = g.off0Y[r][c] * (1 - fallMoveE)
			}

			sel := float32(0)
			if g.sel.r == r && g.sel.c == c {
				sel = 1
			}
			hov := float32(0)
			if g.hoverPos.r == r && g.hoverPos.c == c {
				hov = 1
			}

			s.Uniforms["time"] = float32(g.clock)
			s.Uniforms["selected"] = sel
			s.Uniforms["hover"] = hov
			s.Uniforms["matchProgress"] = float32(g.matchV[r][c])
			s.Uniforms["appear"] = float32(g.appearV[r][c])
			s.Uniforms["impact"] = float32(g.impactV[r][c])

			tlx, tly := g.restTopLeft(r, c)
			s.Move(fyne.NewPos(tlx+float32(ox), tly+float32(oy)))
			s.Refresh()
		}
	}

	g.updateFlows()
	g.updateBursts()
}

// updateBursts positions and animates the active explosions over their centres.
func (g *Game) updateBursts() {
	progress := float32(g.phaseT / clearDur)
	for i := range g.bursts {
		if !g.burstActive[i] {
			continue
		}
		b := g.burstSpecs[i]
		side := float32(b.reach*2) * g.cell
		cx := g.ox + (float32(b.c)+0.5)*g.cell
		cy := g.oy + (float32(b.r)+0.5)*g.cell
		s := g.bursts[i]
		s.Resize(fyne.NewSize(side, side))
		s.Move(fyne.NewPos(cx-side/2, cy-side/2))
		s.Uniforms["time"] = float32(g.clock)
		s.Uniforms["progress"] = progress
		s.Refresh()
	}
}

// updateFlows positions and animates the active match beams over their runs.
func (g *Game) updateFlows() {
	progress := float32(g.phaseT / clearDur)
	for i := range g.flows {
		if !g.flowActive[i] {
			continue
		}
		rd := g.flowRuns[i]
		x := g.ox + float32(rd.c)*g.cell
		y := g.oy + float32(rd.r)*g.cell
		w, h := g.cell, g.cell
		if rd.horiz {
			w = float32(rd.length) * g.cell
		} else {
			h = float32(rd.length) * g.cell
		}
		f := g.flows[i]
		f.Resize(fyne.NewSize(w, h))
		f.Move(fyne.NewPos(x, y))
		f.Uniforms["time"] = float32(g.clock)
		f.Uniforms["progress"] = progress
		f.Refresh()
	}
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// --- input ---------------------------------------------------------------

// Tapped selects a cell, then swaps with an adjacent second tap.
func (g *Game) Tapped(ev *fyne.PointEvent) {
	if g.over || g.phase != phaseIdle {
		return
	}
	cp, ok := g.cellAt(ev.Position)
	if !ok {
		g.sel = cellPos{-1, -1}
		return
	}
	if g.sel.r < 0 {
		g.sel = cp
		return
	}
	if g.sel == cp {
		g.sel = cellPos{-1, -1}
		return
	}
	if adjacent(g.sel.r, g.sel.c, cp.r, cp.c) {
		from := g.sel
		g.sel = cellPos{-1, -1}
		g.startSwap(from, cp)
		return
	}
	g.sel = cp
}

// Dragged accumulates a swipe so a drag from one cell toward a neighbour swaps.
func (g *Game) Dragged(ev *fyne.DragEvent) {
	if g.over || g.phase != phaseIdle {
		return
	}
	if g.dragFrom.r < 0 {
		if cp, ok := g.cellAt(ev.Position); ok {
			g.dragFrom = cp
		}
	}
	g.dragDX += float64(ev.Dragged.DX)
	g.dragDY += float64(ev.Dragged.DY)
}

// DragEnd resolves an accumulated swipe into a swap with the neighbour in the
// dominant drag direction.
func (g *Game) DragEnd() {
	defer func() {
		g.dragFrom = cellPos{-1, -1}
		g.dragDX, g.dragDY = 0, 0
	}()
	if g.phase != phaseIdle || g.dragFrom.r < 0 {
		return
	}
	from := g.dragFrom
	to := from
	if absf(g.dragDX) > absf(g.dragDY) {
		if g.dragDX > 0 {
			to.c++
		} else {
			to.c--
		}
	} else {
		if g.dragDY > 0 {
			to.r++
		} else {
			to.r--
		}
	}
	if absf(g.dragDX) < 4 && absf(g.dragDY) < 4 {
		return // too small to be a swipe
	}
	if inBounds(to.r, to.c) {
		g.sel = cellPos{-1, -1}
		g.startSwap(from, to)
	}
}

// Hover support highlights the cell under the pointer.
func (g *Game) MouseIn(ev *desktop.MouseEvent) { g.MouseMoved(ev) }
func (g *Game) MouseMoved(ev *desktop.MouseEvent) {
	if cp, ok := g.cellAt(ev.Position); ok {
		g.hoverPos = cp
	} else {
		g.hoverPos = cellPos{-1, -1}
	}
}
func (g *Game) MouseOut() { g.hoverPos = cellPos{-1, -1} }

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- renderer ------------------------------------------------------------

func (g *Game) CreateRenderer() fyne.WidgetRenderer {
	g.dragFrom = cellPos{-1, -1}
	objs := make([]fyne.CanvasObject, 0, boardSize*boardSize+maxFlows+maxBursts+1)
	objs = append(objs, g.bg)
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			objs = append(objs, g.shaders[r][c])
		}
	}
	// Beams then bursts are drawn last so the energy and explosions sit on top.
	for i := range g.flows {
		objs = append(objs, g.flows[i])
	}
	for i := range g.bursts {
		objs = append(objs, g.bursts[i])
	}
	r := &gameRenderer{g: g, objs: objs}

	if g.anim == nil {
		g.anim = &fyne.Animation{
			Duration:    time.Second,
			Curve:       fyne.AnimationLinear,
			RepeatCount: fyne.AnimationRepeatForever,
			Tick:        func(float32) { g.tick() },
		}
		g.last = time.Now()
		g.anim.Start()
	}
	return r
}

type gameRenderer struct {
	g    *Game
	objs []fyne.CanvasObject
}

func (r *gameRenderer) Layout(size fyne.Size) {
	g := r.g
	g.size = size
	board := size.Width
	if size.Height < board {
		board = size.Height
	}
	g.cell = board / float32(boardSize)
	g.ox = (size.Width - g.cell*float32(boardSize)) / 2
	g.oy = (size.Height - g.cell*float32(boardSize)) / 2

	g.bg.Resize(size)
	g.bg.Move(fyne.NewPos(0, 0))

	for rr := 0; rr < boardSize; rr++ {
		for cc := 0; cc < boardSize; cc++ {
			s := g.shaders[rr][cc]
			s.Resize(fyne.NewSize(g.cell, g.cell))
			tlx, tly := g.restTopLeft(rr, cc)
			s.Move(fyne.NewPos(tlx, tly))
		}
	}
}

func (r *gameRenderer) MinSize() fyne.Size      { return fyne.NewSize(300, 340) }
func (r *gameRenderer) Refresh()                {}
func (r *gameRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *gameRenderer) Destroy() {
	if r.g.anim != nil {
		r.g.anim.Stop()
	}
}
