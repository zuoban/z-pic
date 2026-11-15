# Design

## Source of truth

- Status: Active
- Date: 2026-11-15
- Surface: Z Pic main compression window and a separate native settings dialog, built with MyGo native UI.
- Evidence: existing MyGo application, native-window screenshots, PNG compression tools, and behavior tests. The current request specifies a settings popup with compression-scheme maintenance, explicit saving, and persistence across application restarts; the main window only selects saved schemes.
- Defaults: one image at a time, JPEG and PNG, Chinese interface, local processing.
- Defaults: single-image or batch JPEG and PNG, Chinese interface, local processing.

## Brand

Quiet, precise, and practical. Show actual file sizes and explicit processing results. Avoid decorative gradients, emoji, fabricated savings, and unrelated tools.

### Polish direction and audit

- Product: local JPEG/PNG compression for people preparing images to share or upload.
- Character: calm, exact, and convenient.
- World: a graphite image workbench, with neutral surfaces and the existing blue accent.
- Moments: the selected image and the measured output size. Do not generate decorative imagery.
- Signature: a quiet image canvas, paired with small keyboard keycaps.

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
6. Preserve preview, scheme selection, results, and actions on the main window; maintain named compression schemes in a separate native settings dialog.
7. Add source/result comparison and discoverable open/compress/save shortcuts, preserving all existing operation guards.
8. Keep visual richness in the actual image and actual output size.
9. Retain only native feedback motion; add no decorative animation.

Validation: existing behavior tests before edits; targeted tests for preset selection, comparison and guarded shortcuts; minimum-window, keyboard, light/dark layout checks; full tests/race/vet/MyGo vet; fresh native-window smoke checks and screenshots.

Workflow reference: unslop by Damir Sokolovsky (@heydamir), CC BY 4.0.

## Product goals

- Select a JPEG or PNG, compress it, compare file sizes, and save a copy or explicitly overwrite the source.
- Support both single-image inspection and sequential batch compression for multi-file and folder selections without inflating idle memory.
- Do not resize images; normalize JPEG EXIF orientation so photos retain their displayed direction. Preserve the source until the user confirms an overwrite.
- Keep the smaller of the original and re-encoded data.
- Non-goals: format conversion, resizing, watermarking, accounts, cloud services, and target-file-size search.
- Success: a first-time user can produce a compressed copy without guessing the next action.

## Personas and jobs

People preparing photographs and screenshots for sharing or uploading. They need an understandable size reduction and a preserved original.

## Information architecture

One main window and one modal child settings window:

- Main window: product header with a “设置” entry; image/empty preview and file details; scrollable saved compression-scheme choices; result summary; primary compress or save action. No parameter sliders or advanced disclosure.
- Main window: product header with a “设置” entry; empty, single-image preview, or batch queue / item preview on the left; scrollable saved compression-scheme choices, batch output-mode toggle (“保存到文件夹 / 覆盖原图”), result summary, and primary compress/continue/save actions on the right. No parameter sliders or advanced disclosure.
- Settings dialog: “设置” heading and a “压缩方案” section; a scheme list with add/delete actions on the left; a scrollable editor for the name, JPEG quality, PNG parameters, and advanced settings on the right; fixed “取消” / “保存” actions and inline feedback.
- The dialog is a parented native modal window (a sheet on macOS). Reopening focuses the existing dialog without resetting edits. Both formats can be configured before selecting an image.
- Save validates and persists the draft list before applying it. Cancel, Escape, and native window close discard edits. The main window remains on screen and resumes after dismissal.

## Design principles

- Keep frequent compression actions on the main window and scheme maintenance in the settings dialog.
- Start with built-in “体积优先 / 均衡 / 画质优先”, defaulting to “均衡”. Built-in schemes are read-only and non-deletable; user-added schemes are named, editable, and deletable. Additions become selectable after saving. Restore saved schemes and the selected scheme on startup.
- Each scheme stores JPEG and PNG parameters independently. Editing a draft does not change the active scheme or its compression result until Save succeeds. Names are trimmed, nonempty, unique, free of control characters, and at most 40 Unicode characters.
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
- Images are contained without cropping. Transparent pixels and unused canvas space show the plain theme background.
- No nonessential animation.

## Components

Use MyGo's native modal child window and built-in radio group, segmented control, collapsible, scroll, text input, row, column, buttons, slider, progress indicator, image, and panels. Show saved schemes as keyboard-operable radio choices in a scrollable region. Application state owns the saved scheme list and selection, dialog draft and editing index, selected image, processing status, result, and message. JPEG quality does not control PNG. Reuse the same theme across both windows; no additional component framework.

Initial settings: size first = JPEG 60, PNG quality 40–65 / 128 colors / speed 3; balanced = JPEG 80, PNG 65–85 / 256 / 3; quality first = JPEG 92, PNG 85–100 / 256 / 1. Save persists the complete scheme list and selected scheme to versioned JSON in the system user configuration directory. Selecting a scheme on the main window also persists that selection.

## Accessibility

Use built-in keyboard-operable widgets, visible focus, descriptive text labels, and semantic theme colors. Show status in text rather than color alone. Make processing and save actions identifiable without icons. Full screen-reader behavior remains subject to platform verification.

On the main window, Command/Ctrl+O selects or replaces an image, Command/Ctrl+Enter compresses, Command/Ctrl+S saves a copy, and Command/Ctrl+, opens settings. Expose shortcut metadata and visible keycaps; the settings button also shows its shortcut in a tooltip. Busy operations, overwrite confirmation, and the settings dialog suppress these main-window shortcuts. In settings, Command/Ctrl+S saves the scheme draft and Escape cancels. Radio groups support arrow keys and one Tab stop. Native modality confines interaction to the settings window while it is open.

## Responsive behavior

Desktop-first: main window defaults to 980 × 720 points, minimum 880 × 660; settings defaults to 820 × 620, minimum 760 × 560. The main window keeps results and actions fixed while long scheme lists scroll. Settings keeps its header and Save/Cancel footer fixed; scheme list and parameter editor scroll independently. Advanced parameters remain keyboard reachable at minimum size. Long names are clipped or truncated with full-value tooltips where available. No mobile target.

## Interaction states

- Empty: supported formats, privacy note, select-image action. Scheme selection and maintenance are usable before an image is selected.
- Empty: supported formats, privacy note, select-image and select-folder actions, plus drag-and-drop for single files, multi-files, or folders. Scheme selection and maintenance are usable before an image is selected.
- Batch queue: virtualized list (`ui.List`) with status filtering (`全部 / 待处理 / 已完成 / 失败`), filename search, add-image, add-folder, clear, remove (button or `Delete` / `Backspace` while the list holds focus), retryable failure recovery (`重试失败项`, selected-item `重试`, and preview `重试` / `移除`, keeping `invalidInput` non-retryable and requiring overwrite confirmation when `覆盖原图` is active), and filtered item preview navigation (`预览` / double-click / Enter / `Esc` return). Header-only validation (`compress.Inspect`) during directory scans limits memory; valid items are processed sequentially on one background worker and saved immediately with collision-free naming (`保存到文件夹`, remembering the last existing `batch_output_dir` in `settings.json` when settings are valid) or confirmed atomic overwrite (`覆盖原图`, skipping non-smaller results). Batch summary surfaces actual reduction, saved byte total, and unchanged counts without overflowing `880×660`. Canceling mid-batch preserves completed items and allows continuing remaining pending items or restarting. Switching active scheme parameters or output mode invalidates prior results.
- Selected: image preview, dimensions, original size, scheme selection.
- Settings: opening clones saved schemes into a draft; modifications, additions, and deletions stay in that draft until Save. Deletion requires inline confirmation and cannot remove the last scheme. Cancel, Escape, and native close discard changes. Reopening does not duplicate the dialog or reset an in-progress draft.
- Save settings: validate every name and parameter; atomically replace the saved configuration before applying it. Failure keeps the dialog, draft, active schemes, and existing result intact and shows an actionable error. Saving edits to another scheme or renaming the active scheme preserves the current result. Changed active compression parameters invalidate it.
- Startup: load saved schemes and selection. Missing configuration uses independent defaults. Invalid configuration reports an error, uses defaults, and leaves the original file intact until the user explicitly saves settings.
- Main-window file drops and image/compression actions are ignored while settings is open, so the dialog cannot discard an edit or start hidden work.
- Processing: status and cancel action; prevent concurrent work.
- Result: original/output sizes, reduction, save-copy action, and a distinct overwrite-original action.
- Result comparison: “对比 / 原图 / 结果” switches the preview without changing the selected bytes, compression settings, export result, or result bitmap. New results default to a centered draggable split, original on the left and output on the right, with measured file-size labels. Both bitmaps share one destination rectangle and use complementary clips so transparent pixels reveal the plain background without showing the other image. The divider supports pointer capture, arrow keys, Home (all output), and End (all original), with native slider accessibility and visible keyboard focus.
- Preview inspection: zoom buttons, mouse wheel, and touchpad pinch share a 1%–1600% scale; “1:1” shows the original dimensions and “适应” resets scale and pan to fit. Touchpad scrolling pans both images together; single-image modes also allow dragging to pan. Pinch and wheel zoom retain the image point under the pointer. Mode changes retain the split, scale, and pan. New selections/results and parameter changes restore fit; busy work, settings, and overwrite confirmation disable preview controls.
- Parameter changes restore the source preview. Once the source is overwritten, hide comparison because the previous source is no longer the selected original; keep the output preview and its camera.
- No reduction: show “未减小（0.0%）”, both sizes, and explain that the original data has been retained.
- Error: inline actionable message; retain a valid previous selection where possible.
- Save copy: native save dialog; cancellation is neutral. The copy API continues to reject the source path and existing files.
- Overwrite: available only for a smaller result. Show an alert with the source path, before/after sizes, and the irreversible effect. Default focus is “取消”; Enter and Escape cancel. The explicit “覆盖原图” choice starts the write.
- Before overwriting, compare the source with the compression snapshot. Reject missing, externally changed, nonregular, or symlink source paths. Write and sync a same-directory temporary file before replacement; preserve source permissions and clean up the temporary file on failure.
- After overwriting, update the selected image bytes, size, and original preview to the saved output. Keep the last comparison and save-copy action, and disable repeated overwrite for that result. Parameter changes and recompression use the newly saved image.
- An overwrite failure retains the result and preview so the user can still save a copy. There is no automatic backup or undo.
- Saving changed active compression parameters or selecting a scheme with different values invalidates the previous result and restores the original preview. A no-op save or rename retains the valid result.
- Disable scheme maintenance during work. Block settings entry while overwrite confirmation is open. Snapshot all parameters on the main thread before compression.
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
- Settings use Go standard-library JSON and file APIs, with a size limit, validation, a synced same-directory temporary file, and atomic replacement. Failed writes preserve the previous file and clean up temporary files. Newly created configuration directories and files are private to the user.
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
- [x] Batch processing implemented: multi-file/folder/CLI/drag-drop entry, sequential execution, collision-free folder export and confirmed atomic overwrite, mid-batch cancel/resume, and single-item preview.
