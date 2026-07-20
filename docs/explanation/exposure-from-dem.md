# Exposure from a DEM (slope & aspect)

The gazetteer's `exposure` block reports the **terrain orientation** at a
coordinate — how steep the ground is (*slope* / Neigung) and which compass
direction it faces (*aspect* / Exposition) — derived from the same Copernicus
elevation DEM that backs `elevation`. This page explains the method and, more
importantly, **how well it works with the Copernicus GLO-30 dataset** and when to
trust it.

## Method

Exposure is a first-order terrain derivative: it needs the local elevation
*gradient*, not just a single height. The service samples a **3×3 window** around
the point (centre + 8 neighbours) via the elevation sampler, offsetting the
longitude/latitude by a fixed **metric** spacing (`sample_spacing_m`, ~30 m to
match GLO-30), and applies **Horn's (1981) finite-difference** scheme — the same
operator GDAL (`gdaldem slope/aspect`) and ESRI use:

```
       | nw  n  ne |          dz/dx = ((ne+2e+se) − (nw+2w+sw)) / (8·s)
window | w   c  e  |          dz/dy = ((nw+2n+ne) − (sw+2s+se)) / (8·s)
       | sw  s  se |          s = sample spacing (m), row 1 = north
```

- `slope = atan(hypot(dz/dx, dz/dy))` → `slope_deg`, and `slope_percent = 100·tan(slope)`.
- The gradient points **uphill**; the aspect is the **downslope** azimuth, i.e.
  the direction the slope faces: `aspect = atan2(−dz/dx, −dz/dy)` normalised to
  0–360° (0 = N, 90 = E), quantised to the 8-point compass rose.

Because longitude spacing shrinks with latitude, the east–west offset is scaled by
`cos(latitude)` so the window stays roughly square on the ground. If the point or
any of the 8 neighbours has no DEM coverage (coast/edge), exposure is `null` — a
reliable gradient needs the whole window.

The implementation lives in `internal/application/gazetteer/exposure.go`
(pure math, unit-tested against synthetic tilted planes) and `service.go`
(`Exposure`, the sampling). It reuses the existing `ElevationSampler` output port,
so no raster-adapter changes were needed.

## How well it works with Copernicus GLO-30

GLO-30 is a global **~30 m** DEM. Two of its properties dominate exposure quality:

### 1. It is a DSM, not a DTM

GLO-30 is a **Digital Surface Model** — it includes vegetation canopy and
buildings, not the bare earth. So over **forest, hedgerows and built-up areas the
slope and aspect describe the canopy/roofs, not the terrain**. On open ground
(fields, moorland, bare rock, alpine slopes) it is a faithful terrain derivative;
under a forest edge it can swing wildly. This is the single biggest caveat: treat
exposure as *surface* orientation, and read it together with land cover.

### 2. Vertical noise vs. the gradient baseline

GLO-30's stated accuracy is ~**4 m LE90 (absolute)**; the *relative* (pixel-to-
pixel) error that actually drives a gradient is smaller, but still on the order of
a metre or two. Over Horn's **60 m baseline** (2 × 30 m), a couple of metres of
noise already produces **~2–4° of spurious slope** and, on otherwise flat ground,
an essentially **random aspect**. Concretely, `atan(2 m / 60 m) ≈ 1.9°`.

Consequence and mitigation:

- **Slope** is trustworthy where it is well above the noise floor — say **≳ 5°**.
  Below that the *value* is still reported but carries a large relative error.
- **Aspect** is meaningful only on a distinct slope. The service therefore treats
  anything below a **flat threshold (~2°)** as `flat: true` and **omits the aspect**
  (`aspect_deg`/`aspect_compass` null/empty) rather than emitting a noise-driven
  direction. `TestComputeExposureNoiseSensitivity` pins this reasoning: 2 m of
  perturbation on a flat window yields ~1.9° of slope.

### 3. Resolution and smoothing

At 30 m, Horn's operator smooths over ~90 m of ground, so it captures hillslope-
scale form but **misses micro-relief** (terraces, gullies, small scarps, narrow
ridgelines). Steep, sharp features read shallower and more rounded than reality.
The `sample_spacing_m` field is returned so a client knows the scale the value
represents.

## Summary — when to trust `exposure`

| Situation | Slope | Aspect |
|---|---|---|
| Open, bare, distinct slope (≳ 5°) | reliable | reliable |
| Gentle terrain (< ~2°) | large relative error | omitted (`flat`) |
| Forest / built-up (DSM) | reflects canopy/roofs, not ground | same caveat |
| Micro-relief finer than ~90 m | smoothed out | smoothed out |
| Coast / DEM edge / no coverage | `null` | `null` |

For bare-earth terrain analysis at finer scales, a dedicated DTM (e.g. a national
1–5 m LiDAR product) wired as the DEM source would improve all of the above; the
method is unchanged — only `sample_spacing_m` would shrink to the new resolution.

See also: [HTTP API — gazetteer](../reference/http-api.md#gazetteer-endpoint),
[Configuration — gazetteer/elevation](../reference/configuration.md).
