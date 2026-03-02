---
name: desktop-icon-generation
description: Regenerate NanoClaw desktop app icons and macOS tray icon from asset PNG files. Use for "换 logo", "重新生成图标", "tray icon 太小/不对" requests.
---

# Desktop Icon Generation (Tauri)

Use this skill when the user wants to update packaged desktop icons (`.app/.dmg`) or the macOS menu bar tray icon.

## Scope

- App/package icons (Tauri bundle icons): `desktop/src-tauri/icons/icon.*` and size variants
- macOS tray icon used by `TrayIconBuilder`: `desktop/src-tauri/icons/trayTemplate.png`

## Inputs

- App icon source: `assets/new-logo.png` (or a user-provided path)
- Tray icon source: `assets/logo-tray.png` (or a user-provided path)

## Workflow

Run from repository root unless noted.

### 1) Validate source images

```bash
sips -g pixelWidth -g pixelHeight -g hasAlpha "assets/new-logo.png"
sips -g pixelWidth -g pixelHeight -g hasAlpha "assets/logo-tray.png"
```

Rules:

- Tauri `icon` command requires square source image.
- For app icon transparency, `hasAlpha: yes` is recommended.
- For tray icon, `hasAlpha: yes` is strongly recommended.

If app icon is not square, create a temporary square image first (center crop):

```bash
sips --cropToHeightWidth 1024 1024 "assets/new-logo.png" --out "assets/new-logo-square.png"
```

Then use `assets/new-logo-square.png` as the source in step 2.

### 2) Regenerate Tauri bundle icons

Run in `desktop/`:

```bash
npm run tauri icon "../assets/new-logo.png"
```

This regenerates:

- `desktop/src-tauri/icons/icon.icns`
- `desktop/src-tauri/icons/icon.ico`
- `desktop/src-tauri/icons/icon.png`
- and other platform-size variants.

If using the cropped temporary file:

```bash
npm run tauri icon "../assets/new-logo-square.png"
```

### 3) Regenerate macOS tray icon file

Create the tray template image at 36x36:

```bash
sips --resampleHeightWidth 36 36 "assets/logo-tray.png" --out "desktop/src-tauri/icons/trayTemplate.png"
```

Confirm it:

```bash
sips -g pixelWidth -g pixelHeight -g hasAlpha "desktop/src-tauri/icons/trayTemplate.png"
```

Expected: `36x36`, with alpha.

### 4) Build / verify

Because tray icon is embedded with `include_image!`, rebuilding is required for changes to take effect.

```bash
cd desktop
npm run tauri build
```

For Rust-side quick check:

```bash
cd desktop/src-tauri
cargo check
```

## Current code assumptions (do not change unless requested)

- macOS tray icon is loaded from:
  - `desktop/src-tauri/icons/trayTemplate.png`
- Tray icon template mode is enabled in:
  - `desktop/src-tauri/src/lib.rs`
  - uses `.icon_as_template(true)` on macOS

## Troubleshooting

### Tray icon appears too small

- Cause: source icon has large transparent padding.
- Fix: provide a tighter source image (larger logo occupancy), then rerun step 3.

### App icon still looks old after build

- Remove old installed app and reinstall the newly built one.
- macOS Finder/Dock cache can lag; relaunch Finder or reboot if needed.

### Unexpected background in icon

- Check whether source PNG actually has opaque pixels (`hasAlpha: no` often means visible background).
- Use a truly transparent PNG source and regenerate.

## Response template to user

When done, report:

- Which source files were used
- Which icon files were regenerated
- Whether alpha/square checks passed
- The exact build command to run (or that it was run)
