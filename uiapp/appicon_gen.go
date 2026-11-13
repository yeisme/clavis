package uiapp

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// GenerateAppIconPNG renders a high-craft macOS squircle application icon at size x size
// (defaulting to 512x512 when size <= 0) using 2x2 supersampled anti-aliasing:
//   - Deep indigo-to-cobalt gradient squircle background with ambient radial glow
//   - Subtle inner rim highlight
//   - Tactile inner keycap frame with a luminous ⌘ (Command) motif
//   - Warm amber/gold & cyan AI spark stars in the upper-right corner
func GenerateAppIconPNG(size int) []byte {
	if size <= 0 {
		size = 512
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	invSize := 2.0 / float64(size)
	aaWidth := invSize * 0.85

	subOffsets := [2]float64{0.25, 0.75}

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var rSum, gSum, bSum, aSum float64
			for _, sy := range subOffsets {
				for _, sx := range subOffsets {
					u := (float64(px)+sx)*invSize - 1.0
					v := (float64(py)+sy)*invSize - 1.0
					sr, sg, sb, sa := sampleAppIcon(u, v, aaWidth)
					rSum += sr * sa
					gSum += sg * sa
					bSum += sb * sa
					aSum += sa
				}
			}
			aAvg := aSum * 0.25
			if aAvg <= 1e-4 {
				continue
			}
			invA := 1.0 / aSum
			rAvg := clampUnit(rSum * invA)
			gAvg := clampUnit(gSum * invA)
			bAvg := clampUnit(bSum * invA)
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(rAvg * 255.0)),
				G: uint8(math.Round(gAvg * 255.0)),
				B: uint8(math.Round(bAvg * 255.0)),
				A: uint8(math.Round(clampUnit(aAvg) * 255.0)),
			})
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func sampleAppIcon(u, v, aa float64) (r, g, b, a float64) {
	// 1. Outer macOS squircle mask (half-extent 0.82, radius 0.36)
	dOuter := sdRoundedRect(u, v, 0.82, 0.82, 0.36)
	outerAlpha := smoothCoverage(dOuter, aa)
	if outerAlpha <= 0 {
		return 0, 0, 0, 0
	}

	// 2. Deep indigo-to-cobalt diagonal gradient background
	tGrad := clampUnit((u + v + 1.64) / 3.28)
	bgR := lerp(0.09, 0.20, tGrad)
	bgG := lerp(0.08, 0.36, tGrad)
	bgB := lerp(0.26, 0.82, tGrad)

	// Ambient cyan/indigo glow near center-bottom-right
	distGlow := math.Hypot(u-0.18, v-0.20)
	glow := math.Exp(-distGlow * distGlow * 2.8)
	bgR = lerp(bgR, 0.28, glow*0.35)
	bgG = lerp(bgG, 0.52, glow*0.42)
	bgB = lerp(bgB, 0.96, glow*0.45)

	// Warm ambient halo around the upper-right AI spark
	distSparkHalo := math.Hypot(u-0.27, v+0.27)
	sparkHalo := math.Exp(-distSparkHalo * distSparkHalo * 7.5)
	bgR = lerp(bgR, 0.98, sparkHalo*0.28)
	bgG = lerp(bgG, 0.72, sparkHalo*0.24)
	bgB = lerp(bgB, 0.24, sparkHalo*0.12)

	// 3. Recessed inner keycap well & beveled frame
	dKeycap := sdRoundedRect(u, v+0.01, 0.54, 0.54, 0.21)
	keycapFill := smoothCoverage(dKeycap, aa)
	if keycapFill > 0 {
		// Darker glass keycap top surface with subtle vertical sheen
		capGrad := clampUnit((v + 0.55) / 1.10)
		capR := lerp(0.14, 0.07, capGrad)
		capG := lerp(0.18, 0.10, capGrad)
		capB := lerp(0.38, 0.24, capGrad)
		bgR = lerp(bgR, capR, keycapFill*0.62)
		bgG = lerp(bgG, capG, keycapFill*0.62)
		bgB = lerp(bgB, capB, keycapFill*0.62)
	}

	// Keycap rim stroke (brighter along top-left bevel)
	dKeycapRing := math.Abs(dKeycap) - 0.014
	keycapRingAlpha := smoothCoverage(dKeycapRing, aa)
	if keycapRingAlpha > 0 {
		bevelLight := clampUnit(0.65 - 0.45*(u+v))
		bgR = lerp(bgR, lerp(0.45, 0.85, bevelLight), keycapRingAlpha*0.65)
		bgG = lerp(bgG, lerp(0.60, 0.92, bevelLight), keycapRingAlpha*0.65)
		bgB = lerp(bgB, 1.0, keycapRingAlpha*0.75)
	}

	// 4. Stylized ⌘ (Command) motif centered slightly lower-left inside the keycap
	cx, cy := u+0.06, v-0.06
	dCmd := sdCommandSymbol(cx, cy, 0.125, 0.068, 0.034)
	cmdAlpha := smoothCoverage(dCmd, aa)
	if cmdAlpha > 0 {
		cmdTint := clampUnit((cy + 0.25) / 0.50)
		cmdR := lerp(0.98, 0.82, cmdTint)
		cmdG := lerp(0.99, 0.93, cmdTint)
		cmdB := 1.00
		bgR = lerp(bgR, cmdR, cmdAlpha)
		bgG = lerp(bgG, cmdG, cmdAlpha)
		bgB = lerp(bgB, cmdB, cmdAlpha)
	}

	// 5. Secondary cyan AI spark star (upper-mid right)
	dCyanStar := sdFourPointStar(u-0.11, v+0.34, 0.075, 0.022)
	cyanAlpha := smoothCoverage(dCyanStar, aa)
	if cyanAlpha > 0 {
		bgR = lerp(bgR, 0.45, cyanAlpha)
		bgG = lerp(bgG, 0.90, cyanAlpha)
		bgB = lerp(bgB, 1.00, cyanAlpha)
	}

	// 6. Primary warm amber/gold AI spark star in the upper-right corner
	sx, sy := u-0.28, v+0.26
	dGoldStar := sdFourPointStar(sx, sy, 0.165, 0.048)
	goldAlpha := smoothCoverage(dGoldStar, aa)
	if goldAlpha > 0 {
		coreDist := math.Hypot(sx, sy) / 0.165
		coreT := clampUnit(coreDist)
		starR := 1.00
		starG := lerp(0.97, 0.74, coreT)
		starB := lerp(0.78, 0.18, coreT)
		bgR = lerp(bgR, starR, goldAlpha)
		bgG = lerp(bgG, starG, goldAlpha)
		bgB = lerp(bgB, starB, goldAlpha)
	}

	// 7. Outer squircle inner rim highlight
	dOuterRim := math.Abs(dOuter+0.012) - 0.010
	rimAlpha := smoothCoverage(dOuterRim, aa) * outerAlpha
	if rimAlpha > 0 {
		rimLight := clampUnit(0.75 - 0.45*(u+v))
		bgR = lerp(bgR, 0.90, rimAlpha*0.38*rimLight)
		bgG = lerp(bgG, 0.95, rimAlpha*0.42*rimLight)
		bgB = lerp(bgB, 1.00, rimAlpha*0.50*rimLight)
	}

	return clampUnit(bgR), clampUnit(bgG), clampUnit(bgB), outerAlpha
}

// GenerateTrayIconPNG renders a crisp anti-aliased monochrome template PNG (default 32x32)
// depicting a rounded keycap outline + key/spark silhouette for macOS/Windows/Linux system trays.
func GenerateTrayIconPNG(size int) []byte {
	if size <= 0 {
		size = 32
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	invSize := 2.0 / float64(size)
	aaWidth := invSize * 0.9

	subOffsets := [2]float64{0.25, 0.75}

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var aSum float64
			for _, sy := range subOffsets {
				for _, sx := range subOffsets {
					u := (float64(px)+sx)*invSize - 1.0
					v := (float64(py)+sy)*invSize - 1.0
					aSum += sampleTrayIconAlpha(u, v, aaWidth)
				}
			}
			aAvg := clampUnit(aSum * 0.25)
			if aAvg > 0 {
				img.SetNRGBA(px, py, color.NRGBA{
					R: 0,
					G: 0,
					B: 0,
					A: uint8(math.Round(aAvg * 255.0)),
				})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func sampleTrayIconAlpha(u, v, aa float64) float64 {
	// Outer keycap rounded rectangle ring
	dCap := math.Abs(sdRoundedRect(u, v, 0.72, 0.58, 0.34)) - 0.08
	capRing := smoothCoverage(dCap, aa)

	// Key bow ring on the left inside the capsule
	dKeyBow := math.Abs(math.Hypot(u+0.26, v)-0.20) - 0.065
	keyBow := smoothCoverage(dKeyBow, aa)

	// Key blade + teeth extending right
	dShaft := sdAxisBox(u-0.16, v, 0.26, 0.065)
	dTooth1 := sdAxisBox(u-0.18, v-0.12, 0.06, 0.09)
	dTooth2 := sdAxisBox(u-0.34, v-0.14, 0.06, 0.11)
	keyBlade := smoothCoverage(math.Min(dShaft, math.Min(dTooth1, dTooth2)), aa)

	// Small 4-point AI spark star in the upper-right
	dSpark := sdFourPointStar(u-0.52, v+0.48, 0.22, 0.065)
	spark := smoothCoverage(dSpark, aa)

	return math.Max(math.Max(capRing, keyBow), math.Max(keyBlade, spark))
}

// sdCommandSymbol computes the exact signed distance to a stroked macOS Command (⌘) glyph:
// straight lines at |x|=b and |y|=b extending to ±(b+r), meeting 270° outer corner loops
// centered at (±(b+r), ±(b+r)) of radius r with C¹ tangent continuity.
func sdCommandSymbol(x, y, b, r, halfStroke float64) float64 {
	ax := math.Abs(x)
	ay := math.Abs(y)
	c := b + r

	// Straight segments: x = b for y in [0, c], and y = b for x in [0, c]
	dVert := math.Hypot(math.Abs(ax-b), math.Max(ay-c, 0))
	dHoriz := math.Hypot(math.Max(ax-c, 0), math.Abs(ay-b))
	dLines := math.Min(dVert, dHoriz)

	// 270° outer loop arc centered at (c, c) of radius r (valid when ax >= c || ay >= c)
	var dLoop float64
	if ax >= c || ay >= c {
		dLoop = math.Abs(math.Hypot(ax-c, ay-c) - r)
	} else {
		// Distance to the arc endpoints (b, c) and (c, b)
		dEnd1 := math.Hypot(ax-b, ay-c)
		dEnd2 := math.Hypot(ax-c, ay-b)
		dLoop = math.Min(dEnd1, dEnd2)
	}

	dCenterline := math.Min(dLines, dLoop)
	return dCenterline - halfStroke
}

// sdFourPointStar computes the signed distance approximation to a 4-pointed star
// with outer tip radius rad and inner waist radius waist.
func sdFourPointStar(x, y, rad, waist float64) float64 {
	ax := math.Abs(x)
	ay := math.Abs(y)
	if ax > rad*1.2 && ay > rad*1.2 {
		return math.Hypot(ax, ay) - rad
	}
	// Smooth astroid-like 4-point star
	p := 0.58
	nx := math.Pow(ax/rad+1e-6, p)
	ny := math.Pow(ay/rad+1e-6, p)
	astroid := (nx + ny - 1.0) * rad * 0.55
	core := math.Hypot(ax, ay) - waist
	return math.Min(astroid, core)
}

func sdRoundedRect(px, py, bx, by, r float64) float64 {
	qx := math.Abs(px) - bx + r
	qy := math.Abs(py) - by + r
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

func sdAxisBox(px, py, bx, by float64) float64 {
	dx := math.Abs(px) - bx
	dy := math.Abs(py) - by
	return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0)
}

func smoothCoverage(dist, aa float64) float64 {
	if aa <= 0 {
		if dist <= 0 {
			return 1
		}
		return 0
	}
	return clampUnit(0.5 - dist/aa)
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
