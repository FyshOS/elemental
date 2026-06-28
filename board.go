package main

import "math/rand"

// board.go holds the match-three rules, free of any rendering. The grid stores a
// material index per cell (0..numTypes-1), or empty for a hole opened by a match.

const (
	boardSize = 8                 // an 8x8 grid of energy cells
	numTypes  = len(materialCount) // number of distinct elements
	empty     = -1
)

// materialCount mirrors the materials slice length without importing it here; it
// is validated against materials at start-up.
var materialCount = [7]struct{}{}

// grid is the logical board: grid[row][col] is a material index or empty.
type grid [boardSize][boardSize]int

// randType returns a random material index.
func randType() int { return rand.Intn(numTypes) }

// newGrid builds a starting board with no pre-existing matches, so the first
// frame is stable and waiting for the player.
func newGrid() grid {
	var g grid
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			for {
				t := randType()
				// Reject a pick that would complete a run of three with the two
				// cells already placed to the left or above.
				if c >= 2 && g[r][c-1] == t && g[r][c-2] == t {
					continue
				}
				if r >= 2 && g[r-1][c] == t && g[r-2][c] == t {
					continue
				}
				g[r][c] = t
				break
			}
		}
	}
	return g
}

// inBounds reports whether (r, c) is on the board.
func inBounds(r, c int) bool {
	return r >= 0 && r < boardSize && c >= 0 && c < boardSize
}

// adjacent reports whether two cells are orthogonal neighbours.
func adjacent(r1, c1, r2, c2 int) bool {
	d := abs(r1-r2) + abs(c1-c2)
	return d == 1
}

// findMatches returns a mask of every cell that is part of a horizontal or
// vertical run of three or more, and how many distinct cells matched.
func (g *grid) findMatches() ([boardSize][boardSize]bool, int) {
	var mask [boardSize][boardSize]bool
	count := 0
	mark := func(r, c int) {
		if !mask[r][c] {
			mask[r][c] = true
			count++
		}
	}

	// Horizontal runs.
	for r := 0; r < boardSize; r++ {
		run := 1
		for c := 1; c < boardSize; c++ {
			if g[r][c] != empty && g[r][c] == g[r][c-1] {
				run++
			} else {
				if run >= 3 {
					for k := 0; k < run; k++ {
						mark(r, c-1-k)
					}
				}
				run = 1
			}
		}
		if run >= 3 {
			for k := 0; k < run; k++ {
				mark(r, boardSize-1-k)
			}
		}
	}

	// Vertical runs.
	for c := 0; c < boardSize; c++ {
		run := 1
		for r := 1; r < boardSize; r++ {
			if g[r][c] != empty && g[r][c] == g[r-1][c] {
				run++
			} else {
				if run >= 3 {
					for k := 0; k < run; k++ {
						mark(r-1-k, c)
					}
				}
				run = 1
			}
		}
		if run >= 3 {
			for k := 0; k < run; k++ {
				mark(boardSize-1-k, c)
			}
		}
	}

	return mask, count
}

// runSpan describes one matched line: its start cell, length, orientation and
// element. The renderer turns each into an energy beam tinted for that element.
type runSpan struct {
	r, c   int
	length int
	horiz  bool
	typ    int
}

// findRuns returns every horizontal and vertical run of three or more identical
// cells as a span, so each can be drawn as a flowing conduit.
func (g *grid) findRuns() []runSpan {
	var runs []runSpan
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; {
			t := g[r][c]
			if t == empty {
				c++
				continue
			}
			j := c + 1
			for j < boardSize && g[r][j] == t {
				j++
			}
			if j-c >= 3 {
				runs = append(runs, runSpan{r: r, c: c, length: j - c, horiz: true, typ: t})
			}
			c = j
		}
	}
	for c := 0; c < boardSize; c++ {
		for r := 0; r < boardSize; {
			t := g[r][c]
			if t == empty {
				r++
				continue
			}
			j := r + 1
			for j < boardSize && g[j][c] == t {
				j++
			}
			if j-r >= 3 {
				runs = append(runs, runSpan{r: r, c: c, length: j - r, horiz: false, typ: t})
			}
			r = j
		}
	}
	return runs
}

// fallMove records that the cell now resting at (row, col) arrived from srcRow.
// A srcRow above the board (negative) marks a freshly materialised cell.
type fallMove struct {
	row, col int
	srcRow   int
	fresh    bool
}

// applyGravity clears every masked cell, lets the survivors fall, and tops each
// column up with new cells. It returns the moves so the renderer can animate the
// drop and the portal arrivals.
func (g *grid) applyGravity(mask [boardSize][boardSize]bool) []fallMove {
	var moves []fallMove
	for c := 0; c < boardSize; c++ {
		// Collect the survivors in this column, top to bottom.
		var survivors []struct {
			t, oldRow int
		}
		for r := 0; r < boardSize; r++ {
			if !mask[r][c] && g[r][c] != empty {
				survivors = append(survivors, struct{ t, oldRow int }{g[r][c], r})
			}
		}

		filled := len(survivors)
		gap := boardSize - filled // number of new cells needed on top

		// Survivors settle at the bottom, preserving order.
		for i, s := range survivors {
			newRow := gap + i
			g[newRow][c] = s.t
			if newRow != s.oldRow {
				moves = append(moves, fallMove{row: newRow, col: c, srcRow: s.oldRow})
			}
		}

		// New cells stream in from above the board.
		for r := 0; r < gap; r++ {
			g[r][c] = randType()
			moves = append(moves, fallMove{row: r, col: c, srcRow: r - gap, fresh: true})
		}
	}
	return moves
}

// hasMoves reports whether any single swap would create a match, so the game can
// reshuffle a dead board.
func (g *grid) hasMoves() bool {
	try := func(r1, c1, r2, c2 int) bool {
		g[r1][c1], g[r2][c2] = g[r2][c2], g[r1][c1]
		_, n := g.findMatches()
		g[r1][c1], g[r2][c2] = g[r2][c2], g[r1][c1]
		return n > 0
	}
	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			if c+1 < boardSize && try(r, c, r, c+1) {
				return true
			}
			if r+1 < boardSize && try(r, c, r+1, c) {
				return true
			}
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
