# Design

## Source of truth

- Status: Active
- Date: 2026-11-15
- Surface: Z Pic desktop window, built with MyGo native UI.
- Evidence: existing MyGo application, a fresh native-window screenshot, PNG compression tools, and 282 passing baseline tests. The user requested a more current visual treatment and easier operation through `$unslop`.
- Defaults: one image at a time, JPEG and PNG, Chinese interface, local processing.

## Brand

Quiet, precise, and practical. Show actual file sizes and explicit processing results. Avoid decorative gradients, emoji, fabricated savings, and unrelated tools.

### Polish direction and audit

- Product: local JPEG/PNG compression for people preparing images to share or upload.
- Character: calm, exact, and convenient.
- World: a graphite image workbench, with neutral surfaces and the existing blue accent.
- Moments: the selected image and the measured output size. Do not generate decorative imagery.
- Signature: a restrained transparency checkerboard, paired with small keyboard keycaps.

This is a polish pass, retaining the product, blue accent, system type, two-column layout, and section order.

Observed issues in `view.go` and the native screenshot:

- Heading, filename, parameter values, and result labels all use weight 700, weakening hierarchy.
- Settings use 10-point gaps while neighboring sections use 12/24, and the bottom caution falls to 11-point text.
- The scheme selector hides the three common choices behind a popup.
- Parameter values compete with the primary action through repeated accent color.
- Output size has no stronger hierarchy than its surrounding labels.
- The preview offers no direct way to compare the selected source with the compressed result.
- Frequent open/compress/save actions expose no keyboard shortcuts.

Implementation plan, in the unslop pass order:

1. Normalize spacing and panel density on a four-point grid.
2. Quiet the surfaces and borders, retaining system light/dark preferences and the blue accent.
3. Keep icons restrained; use one native line illustration only in the empty preview.
4. Shorten supporting copy without removing format or overwrite warnings.
5. Use system type at weights 400–600, with tabular figures for values.
6. Preserve the preview/settings/result/action order; expose presets directly and keep actions outside the settings scroll.
7. Add source/result comparison and discoverable open/compress/save shortcuts, preserving all existing operation guards.
8. Keep visual richness in the actual image and actual output size.
9. Retain only native feedback motion; add no decorative animation.

Validation: existing behavior tests before edits; targeted tests for preset selection, comparison and guarded shortcuts; minimum-window, keyboard, light/dark layout checks; full tests/race/vet/MyGo vet; fresh native-window smoke checks and screenshots.

Workflow reference: unslop by Damir Sokolovsky (@heydamir), CC BY 4.0.

## Product goals

- Select a JPEG or PNG, compress it, compare file sizes, and save a copy or explicitly overwrite the source.
- Do not resize images; normalize JPEG EXIF orientation so photos retain their displayed direction. Preserve the source until the user confirms an overwrite.
- Keep the smaller of the original and re-encoded data.
- Non-goals: batch processing, format conversion, resizing, watermarking, accounts, cloud services, and target-file-size search.
- Success: a first-time user can produce a compressed copy without guessing the next action.

## Personas and jobs

People preparing photographs and screenshots for sharing or uploading. They need an understandable size reduction and a preserved original.

## Information architecture

One window: product header; image/empty preview and file details; compression controls; result summary; primary compress or save action. No navigation sidebar.

## Design principles

- Expose the few parameters that affect compression.
- Offer built-in “体积优先 / 均衡 / 画质优先” schemes and “自定义”, defaulting to “均衡”. Changing a parameter marks the scheme custom. Presets apply settings for both formats; each format shows only its relevant parameters.
- JPEG quality is 1–100. PNG exposes target quality 1–100 and maximum colors 2–256, with minimum quality 0–target and speed 1–11 under “高级设置”. Lowering the target clamps the minimum. Explain that PNG quality is a tool score, not a percentage of quality retained.
- Do not offer a separate PNG lossless compression path. All schemes tune the same lossy pipeline.
- PNG always uses pngquant, then Oxipng to optimize the candidate. Label it “PNG 有损压缩” and explain that colors may change, especially gradients and translucent edges. Accept 8/16-bit inputs without promising to retain channel precision or metadata.
- JPEG orientation is normalized before its metadata is removed.
- Show exact output size after processing; do not predict savings.
- Keep operations responsive and explicitly report errors.

## Visual language

- Follow the desktop's light/dark theme using MyGo's semantic theme colors.
- Use the system font with a restrained hierarchy: window heading, section title, body, secondary labels.
- Spacing: a four-point grid, with 8/12-point control gaps, 16/20-point panel padding, and 24-point page edges.
- A single blue accent for the primary action, active controls, and keyboard focus. Parameter numbers use neutral tabular figures.
- Controls use 8-point corners; panels use 16-point corners. Surfaces and hairlines remain neutral.
- Text weights are 400–600; output size gets a stronger scale than supporting labels.
- Images are contained without cropping. Transparency uses a checkerboard preview.
- No nonessential animation.

## Components

Use MyGo's built-in radio group, segmented control, collapsible, scroll, text, row, column, buttons, slider, progress indicator, image, and panels. Show the three schemes as keyboard-operable radio segments; when parameters are custom, none is selected and the heading states “自定义”. Application state owns the selected path, scheme, JPEG quality, PNG parameters, processing status, result, and message. JPEG quality does not control PNG. Styling stays with the window view; no additional component framework.

Preset settings: size first = JPEG 60, PNG quality 40–65 / 128 colors / speed 3; balanced = JPEG 80, PNG 65–85 / 256 / 3; quality first = JPEG 92, PNG 85–100 / 256 / 1. Custom values persist within the session; disk persistence is outside the current scope.

## Accessibility

Use built-in keyboard-operable widgets, visible focus, descriptive text labels, and semantic theme colors. Show status in text rather than color alone. Make processing and save actions identifiable without icons. Full screen-reader behavior remains subject to platform verification.

Command/Ctrl+O selects or replaces an image, Command/Ctrl+Enter compresses, and Command/Ctrl+S saves a copy. Expose shortcut metadata and visible keycaps. Busy operations and overwrite confirmation suppress window shortcuts. Radio groups support arrow keys and one Tab stop.

## Responsive behavior

Desktop-first, default 980 × 720 points, minimum 880 × 660. Keep the scheme selector, result summary, status, and actions outside the scrolling parameter region. Advanced settings scroll without pushing save/compress below the window. Prefer filling available space and wrapping long file names. No mobile target.

## Interaction states

- Empty: supported formats, privacy note, select-image action. Format-specific parameters wait until an image is selected.
- Selected: image preview, dimensions, original size, relevant controls.
- Processing: status and cancel action; prevent concurrent work.
- Result: original/output sizes, reduction, save-copy action, and a distinct overwrite-original action.
- Result comparison: “原图 / 压缩后” switches the displayed bitmap without changing the selected bytes, compression settings, export result, or result bitmap. New results default to “压缩后”. Parameter changes restore the source preview. Once the source is overwritten, hide comparison because the previous source is no longer the selected original.
- No reduction: show “未减小（0.0%）”, both sizes, and explain that the original data has been retained.
- Error: inline actionable message; retain a valid previous selection where possible.
- Save copy: native save dialog; cancellation is neutral. The copy API continues to reject the source path and existing files.
- Overwrite: available only for a smaller result. Show an alert with the source path, before/after sizes, and the irreversible effect. Default focus is “取消”; Enter and Escape cancel. The explicit “覆盖原图” choice starts the write.
- Before overwriting, compare the source with the compression snapshot. Reject missing, externally changed, nonregular, or symlink source paths. Write and sync a same-directory temporary file before replacement; preserve source permissions and clean up the temporary file on failure.
- After overwriting, update the selected image bytes, size, and original preview to the saved output. Keep the last comparison and save-copy action, and disable repeated overwrite for that result. Parameter changes and recompression use the newly saved image.
- An overwrite failure retains the result and preview so the user can still save a copy. There is no automatic backup or undo.
- Changing any compression parameter or applying a preset with different values invalidates the previous result and restores the original preview. A no-op setting change retains the valid result.
- Disable scheme and parameter changes during work. Snapshot all parameters on the main thread before compression.
- PNG always attempts color quantization without requiring a mode choice.
- A pngquant quality-gate refusal or a non-smaller result retains the original as the candidate for the final optimization step. Never export a larger result.
- Compression uses the bytes captured during selection, so later edits by another application do not change the selected image.
- Cancellation is checked again when delivering a background result to the main thread.
- Normal application shutdown waits for a file write already in progress.
- Offline: all processing works locally.

## Content voice

Concise Chinese. Use “压缩图片”, “JPEG 质量”, “另存副本”, and “覆盖原图”. Do not claim that the original is always preserved. Use “PNG 有损压缩” and explain the impact on colors beside its controls. Do not promise a smaller file.

## Implementation constraints

- MyGo native UI, pinned dependency and Go toolchain.
- Standard-library JPEG encoding and PNG validation; bundled Oxipng and pngquant CLI tools for PNG. No additional Go dependencies.
- Native tools operate on an immutable byte snapshot through stdin/stdout, never the source path. Executables are bundled for the verified target, with pinned versions and third-party notices.
- Maximum input: 64 MiB and 32 million pixels.
- Background processing; serialize image operations to limit memory use.
- Check cancellation before and after codecs; cancel external PNG processes with their context. Standard JPEG encoding does not guarantee immediate interruption.
- Unit tests must cover file preservation, format behavior, invalid input, and cancellation. Build and native-window smoke checks are required before claiming runtime readiness.
- Overwrite tests must cover confirmation/cancellation, source-change detection, write failure, temporary-file cleanup, permissions, and the updated UI snapshot. The supported macOS path uses atomic same-directory rename; the final source check and rename are separate operations and do not constitute a cross-process compare-and-swap.

## Open questions

- [ ] Owner: user. Which operating systems should receive release installers? The first build and runtime check target the available macOS machine.
- [ ] Owner: user. Is batch processing needed next? It is excluded from this first release.
