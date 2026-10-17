# Branding

Source artwork and the generated icon resources.

| File | What it is |
| --- | --- |
| `novabar.ico` | Multi-resolution Windows icon, 16–256px. Committed. |
| `novabar-256.png` | The 256px frame as a PNG, for docs. |
| `wordmark-dark.png` | The "NovaBar" wordmark on the dark pill, transparent background. Used as the README banner. |
| `novabar.rc` | Resource script. One line: `1 ICON "novabar.ico"`. |
| `novabar.syso` | The compiled resource, copied into `cmd/daemon/` and `cmd/shellctl/`. |

## The wordmark is mark plus text, and no "+"

The source sheet carries lockups reading "NovaBar+". The product is `NovaBar`: the module
is `github.com/ngbcoder/NovaBar`, the binary is `novabar.exe`, and the README says
NovaBar. `wordmark-dark.png` is cut from a corrected sheet with the suffix removed.

The banner is the **dark** pill on transparency rather than the cream one. The cream
lockup is a bright block on GitHub's light theme and an unpleasant glare on the dark one;
the dark pill reads as a badge on light and blends on dark. It is also the lockup the
design intends, since the sheet presents it that way.

Regenerate it after an artwork change by pointing the cropper at the dark pill's coarse
bounds, measured from the luminance band that isolates tile fill from the page:

```
# dark pill occupies roughly x 1040..2240, y 150..660 in the 2400x1360 sheet
crop <sheet>.png branding/wordmark-dark.png 1030 140 2250 670 640
```

The tool measures the block's exact bounds and corner radius itself; the arguments are only
the coarse search region.

## Not wired: a webview favicon

There is deliberately no favicon route. The widgets render in WebView2 with no tab strip,
so a favicon is never displayed — serving one would be a route nothing can see. The
window icon comes from the `.syso` resource instead, which is what the taskbar, Alt-Tab and
Explorer actually read.

## The icon is the mark only

The app icon is cropped to the standalone spark tile, not to a wordmark lockup. Two
reasons: an app icon containing 8pt text is unreadable at 16px in the taskbar, and the
lockups in the source sheet carry a `+` suffix that the product does not use — the module
is `github.com/ngbcoder/NovaBar` and the README says NovaBar.

Because only the mark is used, changing the wordmark does not require regenerating the
icon.

## Why `novabar.syso` is committed

Go's linker embeds `.syso` files found in a package directory, so committing the compiled
resource means `go build` picks up the icon with no toolchain on the machine. That matters
here specifically: `build.ps1 verify` asserts the tree builds with `CGO_ENABLED=0` on a
machine with no C compiler, and generating the resource at build time would require
`windres` and break that.

The cost is an opaque 410KB binary in the tree. It is regenerated only when the artwork
changes, and the command to do so is below.

## Regenerating

Geometry measured from `Logo (glow, amber)@2x.png`, a 2400x1360 sheet holding five
variants. The app icon is the top-left tile:

- tile bounds `x 159..879, y 159..879` → 721x721
- corner radius ≈153px, 21.2% of the tile width
- tile fill `#1A1310`, sheet background `#0C0908`

Measured rather than eyeballed, because the tile and the sheet background differ by only
about 11 in luma. A colour threshold cannot separate them, and the tile carries a radial
gradient that would make a threshold-derived alpha mask fade out the interior. The
rounded-square alpha is instead computed from the signed distance to a rounded rectangle
with the measured radius, which anti-aliases exactly and leaves the artwork untouched.

The downscale from 721 to 256 and below is an area average weighted by alpha coverage.
Averaging straight RGBA would drag transparent black out of the corners and leave a dark
halo, which is the one artefact the rounded mask exists to prevent.

To rebuild after an artwork change:

```sh
# 1. Crop and generate the .ico and PNG. The geometry constants live in the generator.
#    Remeasure them first if the tile moved:
#      - tile fill luma band isolates the tile from the sheet
#      - top row run length gives the corner radius as (width - runWidth) / 2

# 2. Compile the resource with mingw windres (or llvm-rc on Windows)
windres -i branding/novabar.rc -O coff -o branding/novabar.syso

# 3. Copy into both command packages
cp branding/novabar.syso cmd/daemon/novabar.syso
cp branding/novabar.syso cmd/shellctl/novabar.syso
```

The `.ico` stores each frame as a 32-bit BMP DIB rather than a PNG payload. PNG-in-ICO
works on Vista and later, but a desktop shell should not be the component that discovers
which format a consumer accepts.

## Verifying

```powershell
.\build.ps1 build
# confirm the resource reached the binary
Select-String -Path bin\novabar.exe -Pattern '.rsrc' -SimpleMatch -Encoding Byte -Quiet
```

`build.ps1 verify` covers the part that matters: it builds the tree with
`CGO_ENABLED=0`, which fails if the `.syso` ever stops being picked up by the linker.