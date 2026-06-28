package main

import "fyne.io/fyne/v2/canvas"

// This file builds the GLSL fragment shaders that turn each board cell into a
// living, procedural "energy cell". Everything you see in Elemental - the
// glowing hexagons, the dissolve, the portal materialise, the rippling
// background - is computed on the GPU. There is no sprite art at all.
//
// A canvas.Shader is handed its source verbatim, so each program is assembled
// from three parts: a version/precision header, a shared block of helper
// functions, and a per-material body. Desktop OpenGL wants "#version 110" while
// the GLES/mobile/web target wants "#version 100" plus precision qualifiers -
// the body in between is identical, which keeps the materials in one place.

// glHeaderDesktop is the prelude for desktop OpenGL (core/compat profile).
const glHeaderDesktop = "#version 110\n"

// glHeaderES is the prelude for OpenGL ES, mobile and web targets.
const glHeaderES = `#version 100
#ifdef GL_ES
# ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
# else
precision mediump float;
# endif
precision mediump int;
#endif
`

// cellUniforms is the uniform contract for a board cell. frame_size and
// rect_coords are supplied automatically by Fyne; the rest are driven per tile
// each frame from the cell's Uniforms map.
const cellUniforms = `
uniform vec2 frame_size;    // output frame size in pixels (set by Fyne)
uniform vec4 rect_coords;   // this tile's bounds x1,x2,y1,y2 in pixels (set by Fyne)
uniform float time;         // global animation clock, seconds
uniform float selected;     // 1 when this cell is picked up
uniform float matchProgress;// 0..1 dissolve while clearing a match
uniform float appear;       // 0..1 portal materialise (1 = settled)
uniform float hover;        // 1 when the pointer is over this cell
uniform float impact;       // 0..1 landing smash, decays after a drop
`

// bgUniforms is the uniform contract for the full-window background.
const bgUniforms = `
uniform vec2 frame_size;    // output frame size in pixels (set by Fyne)
uniform float time;         // global animation clock, seconds
uniform float rippleTime;   // seconds since the last match (drives the shock wave)
uniform float rippleX;      // ripple centre, normalised 0..1
uniform float rippleY;
uniform float combo;        // current cascade depth, intensifies the reaction
`

// glslHelpers are procedural-texture building blocks shared by every material:
// value noise, fractal Brownian motion, a hexagon distance field and an HSV
// helper. They are pure functions of position and time.
const glslHelpers = `
float hash21(vec2 p) {
    p = fract(p * vec2(123.34, 345.45));
    p += dot(p, p + 34.345);
    return fract(p.x * p.y);
}

float noise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    float a = hash21(i);
    float b = hash21(i + vec2(1.0, 0.0));
    float c = hash21(i + vec2(0.0, 1.0));
    float d = hash21(i + vec2(1.0, 1.0));
    return mix(mix(a, b, f.x), mix(c, d, f.x), f.y);
}

float fbm(vec2 p) {
    float v = 0.0;
    float a = 0.5;
    for (int i = 0; i < 5; i++) {
        v += a * noise(p);
        p *= 2.02;
        a *= 0.5;
    }
    return v;
}

// Signed distance to a flat-top hexagon of "radius" r, negative inside.
float hexSDF(vec2 p, float r) {
    const vec3 k = vec3(-0.866025404, 0.5, 0.577350269);
    p = abs(p);
    p -= 2.0 * min(dot(k.xy, p), 0.0) * k.xy;
    p -= vec2(clamp(p.x, -k.z * r, k.z * r), r);
    return length(p) * sign(p.y);
}

vec3 hsv2rgb(vec3 c) {
    vec4 K = vec4(1.0, 2.0 / 3.0, 1.0 / 3.0, 3.0);
    vec3 p = abs(fract(c.xxx + K.xyz) * 6.0 - K.www);
    return c.z * mix(K.xxx, clamp(p - K.xxx, 0.0, 1.0), c.y);
}
`

// cellMain wraps every material body. Each material supplies a function
//
//	vec3 element(vec2 uv, vec2 p, float t)
//
// returning straight emissive colour, and this shared main() carves it into a
// glowing hexagon and layers on selection, dissolve and materialise effects.
const cellMain = `
void main() {
    // Reconstruct this tile's local coordinates from the pixel rectangle.
    float left = rect_coords[0];
    float right = rect_coords[1];
    float yb = frame_size.y - rect_coords[3];
    float yt = frame_size.y - rect_coords[2];
    vec2 sz = vec2(right - left, yt - yb);
    vec2 uv = (gl_FragCoord.xy - vec2(left, yb)) / sz;
    vec2 p = uv * 2.0 - 1.0;            // -1..1, centred on the tile
    float t = time;

    float ap = clamp(appear, 0.0, 1.0);
    float mp = clamp(matchProgress, 0.0, 1.0);

    // Portal materialise: the cell spins up and grows out of nothing.
    float grow = mix(0.25, 1.0, ap * ap * (3.0 - 2.0 * ap));
    float ang = (1.0 - ap) * 6.2831;
    float cs = cos(ang), sn = sin(ang);
    vec2 pc = mat2(cs, -sn, sn, cs) * p / grow;

    // Landing smash: the cell gives a subtle wide-and-flat squash on impact.
    float imp = clamp(impact, 0.0, 1.0);
    pc.x *= (1.0 - 0.10 * imp);
    pc.y *= (1.0 + 0.14 * imp);

    // Selection makes the cell breathe; a match shrinks it as it dissolves.
    float pulse = selected * 0.05 * sin(t * 8.0);
    float r = (0.80 + pulse) * (1.0 - 0.30 * mp);

    float d = hexSDF(pc, r);
    float aa = 2.5 / sz.y;
    float inside = smoothstep(aa, -aa, d);

    // Dissolve into particles: noise gates which fragments survive.
    float diss = 1.0;
    if (mp > 0.001) {
        float g = noise(pc * 7.0 + 17.0);
        diss = smoothstep(mp - 0.08, mp + 0.08, g);
    }

    vec3 col = element(uv, pc, t);

    // Rim light around the hexagon edge.
    float rim = smoothstep(0.18, 0.0, abs(d));
    col += rim * (0.35 * col + 0.1);

    // Cyan selection ring tracing the border.
    col += selected * vec3(0.5, 0.85, 1.0) * smoothstep(0.05, 0.0, abs(d));

    // White-hot energy flash that flickers as the match resolves.
    float flash = mp * (0.6 + 0.4 * sin(t * 28.0));
    col = mix(col, vec3(1.6, 1.9, 2.4), clamp(flash, 0.0, 1.0) * 0.6);

    // Impact flash brightens the cell the instant it lands.
    col *= 1.0 + 0.5 * imp;
    col += imp * 0.3;

    // Coloured halo bleeding past the edge so the dark grid glows.
    float halo = exp(-max(d, 0.0) * 7.0);
    vec3 hc = element(vec2(0.5, 0.5), vec2(0.0, 0.0), t);

    float a = inside * diss;
    col = mix(hc, col, a);
    a = clamp(max(a, halo * 0.5), 0.0, 1.0);
    a *= ap;                              // fade in with the portal
    col += hover * 0.12;

    gl_FragColor = vec4(col, a);
}
`

// material holds one energy-cell type: a stable name (used to key the compiled
// GL program) and the GLSL body computing its colour.
type material struct {
	name string
	body string
}

// materials defines the seven elements. Players learn to read them by their
// motion and palette, not by any label - the shader is the sprite.
var materials = []material{
	{"plasma", `
vec3 element(vec2 uv, vec2 p, float t) {
    float n = fbm(p * 2.0 + vec2(t * 0.3, t * 0.2));
    n += 0.5 * fbm(p * 4.0 - t * 0.25);
    float v = sin(n * 6.2831 + t * 2.0) * 0.5 + 0.5;
    // A light hot-pink palette: even the trough stays bright magenta so plasma
    // never sinks into the nebula's deep purple.
    vec3 deep = vec3(0.60, 0.12, 0.58);
    vec3 a = vec3(0.95, 0.30, 0.82);
    vec3 b = vec3(1.00, 0.66, 0.96);
    vec3 col = mix(deep, mix(a, b, v), v);
    col += pow(v, 3.0) * 0.8;
    col += 0.15;                       // overall lift to keep the pink luminous
    return col;
}
`},
	{"arc", `
vec3 element(vec2 uv, vec2 p, float t) {
    float arcs = 0.0;
    for (int i = 0; i < 3; i++) {
        float fi = float(i);
        float wob = fbm(p * 3.0 + fi + t);
        float y = p.y + 0.30 * sin(p.x * 3.0 + t * 4.0 + fi * 2.1) * wob;
        arcs += 0.02 / (abs(y - (fi - 1.0) * 0.4) + 0.02);
    }
    float flick = 0.6 + 0.4 * noise(vec2(t * 10.0, 0.0));
    vec3 col = vec3(0.08, 0.20, 0.45) * 0.4;
    col += vec3(0.55, 0.85, 1.0) * arcs * flick;
    return col;
}
`},
	{"metal", `
vec3 element(vec2 uv, vec2 p, float t) {
    float n = fbm(p * 3.0 + t * 0.4);
    float m = fbm(p * 6.0 - t * 0.3 + n);
    float spec = pow(0.5 + 0.5 * sin((m + n) * 6.2831 + t), 8.0);
    vec3 base = mix(vec3(0.14, 0.16, 0.22), vec3(0.62, 0.68, 0.78), m);
    base += spec * vec3(1.0);
    return base;
}
`},
	{"lava", `
vec3 element(vec2 uv, vec2 p, float t) {
    float n = fbm(p * 3.0 + vec2(0.0, -t * 0.5));
    n += 0.5 * fbm(p * 7.0 + t * 0.2);
    float crust = smoothstep(0.45, 0.62, n);
    vec3 hot = mix(vec3(1.0, 0.7, 0.1), vec3(1.0, 0.2, 0.0), n);
    vec3 col = mix(hot, vec3(0.12, 0.04, 0.03), crust);
    col += pow(max(0.0, n - 0.5), 2.0) * vec3(2.0, 0.8, 0.2);
    return col;
}
`},
	{"crystal", `
vec3 element(vec2 uv, vec2 p, float t) {
    // Irregular facets via a Voronoi whose seed points drift, so the crystal
    // refracts and shifts instead of sitting on a static grid.
    vec2 g = p * 2.5;
    vec2 ip = floor(g);
    vec2 fp = fract(g);
    float minD = 10.0;
    float secondD = 10.0;
    vec2 cellId = vec2(0.0);
    for (int j = -1; j <= 1; j++) {
        for (int i = -1; i <= 1; i++) {
            vec2 o = vec2(float(i), float(j));
            vec2 rnd = vec2(hash21(ip + o), hash21(ip + o + 5.2));
            vec2 pt = o + 0.5 + 0.4 * sin(t * 0.8 + 6.2831 * rnd);
            float d = length(fp - pt);
            if (d < minD) {
                secondD = minD;
                minD = d;
                cellId = ip + o;
            } else if (d < secondD) {
                secondD = d;
            }
        }
    }
    float facet = hash21(cellId);
    float shade = 0.45 + 0.5 * facet;
    vec3 col = hsv2rgb(vec3(0.5 + 0.18 * facet, 0.5, shade));

    // Sharp facet boundaries catch the light.
    float border = 1.0 - smoothstep(0.0, 0.06, secondD - minD);
    col += border * 0.45;

    // Travelling specular glimmer that sparkles across the facets.
    float glim = pow(0.5 + 0.5 * sin(t * 3.0 + facet * 30.0 + (p.x + p.y) * 4.0), 20.0);
    col += glim * vec3(1.0);
    return col;
}
`},
	{"nebula", `
vec3 element(vec2 uv, vec2 p, float t) {
    float n = fbm(p * 2.0 + t * 0.05);
    float n2 = fbm(p * 4.0 - t * 0.07 + n);
    vec3 col = mix(vec3(0.02, 0.0, 0.10), vec3(0.30, 0.10, 0.60), n);
    col = mix(col, vec3(0.75, 0.20, 0.55), n2 * n2);
    float s = hash21(floor(p * 30.0));
    s = step(0.985, s) * (0.5 + 0.5 * sin(t * 3.0 + s * 30.0));
    col += s;
    return col;
}
`},
	{"hologram", `
vec3 element(vec2 uv, vec2 p, float t) {
    float scan = 0.5 + 0.5 * sin(uv.y * 40.0 - t * 6.0);
    float grid = step(0.9, fract(uv.x * 12.0)) + step(0.9, fract(uv.y * 12.0));
    float glitch = step(0.98, noise(vec2(floor(uv.y * 20.0), t * 5.0)));
    vec3 col = vec3(0.10, 1.0, 0.70) * (0.4 + 0.6 * scan);
    col += grid * 0.2 * vec3(0.2, 1.0, 0.8);
    col += glitch * vec3(1.0);
    return col;
}
`},
}

// bgMain is the full-window background: a slow nebula, a faint hex lattice and a
// shock wave that radiates from each match, intensified by combo depth.
const bgMain = `
void main() {
    vec2 res = frame_size;
    vec2 uv = gl_FragCoord.xy / res;
    vec2 p = (gl_FragCoord.xy - 0.5 * res) / res.y;
    float t = time;

    // Drifting nebula haze.
    float n = fbm(p * 2.0 + t * 0.03);
    float n2 = fbm(p * 4.0 - t * 0.04 + n);
    vec3 col = mix(vec3(0.02, 0.02, 0.05), vec3(0.05, 0.03, 0.12), n);
    col += vec3(0.10, 0.05, 0.20) * n2 * n2;

    // Faint hexagonal lattice glow.
    vec2 hp = p * 9.0;
    float hd = abs(hexSDF(fract(hp) - 0.5, 0.42));
    col += smoothstep(0.06, 0.0, hd) * vec3(0.04, 0.06, 0.12);

    // Expanding shock wave from the last match.
    float rd = distance(uv, vec2(rippleX, rippleY));
    float wave = sin(rd * 40.0 - rippleTime * 12.0)
        * exp(-rd * 5.0) * exp(-rippleTime * 2.5);
    float power = 0.3 + 0.25 * combo;
    col += wave * power * vec3(0.3, 0.6, 1.0);

    // Vignette to focus the board.
    col *= 1.0 - 0.5 * dot(p, p);

    gl_FragColor = vec4(col, 1.0);
}
`

// newCellShader builds a Shader for the given material. Tiles created with the
// same material share a Name, so Fyne compiles each program only once and reuses
// it across the whole board.
func newCellShader(m material) *canvas.Shader {
	desktop := glHeaderDesktop + cellUniforms + glslHelpers + m.body + cellMain
	es := glHeaderES + cellUniforms + glslHelpers + m.body + cellMain
	s := canvas.NewShader("elemental_cell_"+m.name, []byte(desktop), []byte(es))
	s.Uniforms = map[string]float32{
		"time": 0, "selected": 0, "matchProgress": 0, "appear": 1, "hover": 0, "impact": 0,
	}
	return s
}

// materialGlow is a representative emissive colour per element, aligned with the
// materials slice. The match beam is tinted with it so the energy that flows
// between cells reads as the element they are made of.
var materialGlow = [...][3]float32{
	{1.00, 0.55, 0.95}, // plasma   - hot pink
	{0.55, 0.85, 1.00}, // arc      - electric cyan
	{0.72, 0.80, 0.92}, // metal    - steel
	{1.00, 0.45, 0.10}, // lava     - molten orange
	{0.55, 0.95, 0.95}, // crystal  - teal
	{0.62, 0.30, 0.95}, // nebula   - violet
	{0.20, 1.00, 0.70}, // hologram - green
}

// beamUniforms is the contract for the match beam.
const beamUniforms = `
uniform vec2 frame_size;
uniform vec4 rect_coords;
uniform float time;
uniform float progress;   // 0..1 over the clear
uniform float horiz;      // 1 for a horizontal run, 0 for vertical
uniform float cr;         // element colour
uniform float cg;
uniform float cb;
`

// beamMain draws a glowing energy conduit along a matched run: pulses race in
// from both ends and burst where the cells meet, then the whole thing fades.
const beamMain = `
void main() {
    float left = rect_coords[0];
    float right = rect_coords[1];
    float yb = frame_size.y - rect_coords[3];
    float yt = frame_size.y - rect_coords[2];
    vec2 sz = vec2(right - left, yt - yb);
    vec2 uv = (gl_FragCoord.xy - vec2(left, yb)) / sz;
    float t = time;

    float axis = mix(uv.y, uv.x, horiz);    // along the run
    float across = mix(uv.x, uv.y, horiz);  // perpendicular

    // Bright core line down the middle of the run.
    float core = exp(-pow((across - 0.5) * 6.0, 2.0));

    // Pulses travelling inward toward the meeting point.
    float dc = abs(axis - 0.5);
    float pulse = 0.5 + 0.5 * sin(dc * 30.0 - t * 16.0);
    float flow = core * (0.5 + 0.8 * pulse);

    // Burst of energy where the cells meet, plus a fine sparkle along the line.
    float meet = exp(-pow(dc * 4.0, 2.0));
    flow += core * meet * 1.5;
    flow += core * 0.3 * (sin(axis * 60.0 - t * 25.0) * 0.5 + 0.5);

    // Envelope: the energy gathers, peaks, then dissolves with the match.
    float env = sin(clamp(progress, 0.0, 1.0) * 3.14159);

    vec3 tint = vec3(cr, cg, cb);
    vec3 col = tint * flow + tint * meet * env * 0.5;
    float a = clamp(flow * env, 0.0, 1.0);
    gl_FragColor = vec4(col, a);
}
`

// newBeamShader builds the shared match-beam shader. All beams use one program;
// colour and orientation are per-instance uniforms.
func newBeamShader() *canvas.Shader {
	desktop := glHeaderDesktop + beamUniforms + glslHelpers + beamMain
	es := glHeaderES + beamUniforms + glslHelpers + beamMain
	s := canvas.NewShader("elemental_beam", []byte(desktop), []byte(es))
	s.Uniforms = map[string]float32{
		"time": 0, "progress": 0, "horiz": 1, "cr": 1, "cg": 1, "cb": 1,
	}
	s.Hide()
	return s
}

// newBackgroundShader builds the full-window reactive background.
func newBackgroundShader() *canvas.Shader {
	desktop := glHeaderDesktop + bgUniforms + glslHelpers + bgMain
	es := glHeaderES + bgUniforms + glslHelpers + bgMain
	s := canvas.NewShader("elemental_background", []byte(desktop), []byte(es))
	s.Uniforms = map[string]float32{
		"time": 0, "rippleTime": 100, "rippleX": 0.5, "rippleY": 0.5, "combo": 0,
	}
	return s
}
