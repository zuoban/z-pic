# Design

## Source of truth

- Status: Active
- Date: 2026-11-16
- Surface: Z Pic main compression window, native settings dialog, and an optional always-on-top image drop window, built with MyGo native UI.
- Evidence: existing MyGo application, native-window screenshots, image compression tools, behavior tests, and the supplied macOS floating image-stack/gallery references. Settings maintain saved compression schemes in a separate popup; the current floating-panel refinement follows the two reference states while preserving image import and compression behavior.
- Defaults: single-image or batch static JPEG, PNG and WebP, Chinese interface, local processing.

## Brand

Quiet, precise, and practical. Show actual file sizes and explicit processing results. Avoid decorative gradients, emoji, fabricated savings, and unrelated tools.

### Polish direction and audit

- Product: local JPEG/PNG/WebP compression for people preparing images to share or upload.
- Character: calm, exact, and convenient.
- World: a graphite image workbench, with neutral surfaces and the existing blue accent.
- Moments: the selected image and the measured output size. Do not generate decorative imagery.
- Signature: paired source → output file sizes, with tabular figures and quiet keyboard keycaps.

This is a polish pass, retaining the product, blue accent, system type, two-column layout, and section order.

The current native-window audit identified light slop and consistency problems:

- Format and local-processing copy repeated across the header, empty canvas and footer.
- Peer preview and scheme headings used different sizes; the scheme region and bottom actions had an uneven rhythm.
- Scheme, settings, batch and floating selections used unrelated treatments, including solid blue gallery cells.
- Batch output used 10-point padding and a 30-point segmented control outside the four-point scale.
- Summary titles competed with savings and failure counts on the same line.
- Batch mode advertised “另存” for a shortcut that chooses the output folder.
- The fixed 880-point minimum and crowded batch-preview toolbar limited narrow-window use.

Applied refinements, in the unslop pass order:

1. Use four-point spacing, bounded scrollable scheme lists and stable bottom actions; fit the scheme list before assigning spare space.
2. Retain graphite light/dark surfaces and blue #2563eb, with primary, secondary and metadata text roles, quiet 8%-opacity hairlines, and restrained selection fills.
3. Keep contextual image illustrations; render normal floating action icons at 16 points with an actual 1.5-point stroke.
4. Consolidate empty-state format/limit copy, shorten supporting text, and show mode-specific shortcut hints while preserving loss and overwrite warnings.
5. Keep system type at weights 400–600, align peer headings at 16 points, and use tabular figures with compact sizes that retain the displayed units.
6. Keep the existing two-column workspace, settings sheet and floating states; adapt page padding, control-column width and comparison-tool rows to narrower windows.
7. Share a light accent tint and outline across selections, retain separate keyboard focus, give disabled primary actions a neutral fill, and place summary status below the size comparison.
8. Keep visual richness in real source images, before/after inspection and measured output sizes; add no generated images or invented data.
9. Preserve native interaction feedback without adding decorative motion.

Validation completed: the full Go suite plus temporary light/dark visual scenarios passed (741 checks across five packages), MyGo vet passed, and the native automation build ran successfully. Layout scenarios covered 760-, 880- and 980-point regression cases, 760/980/1440-point visual cases, minimum settings, long paths, failures and floating states. Native smoke checks exercised actual JPEG compression (517.3 KiB → 134.7 KiB), zoom/fit, settings duplication/cancellation without losing results, filename search/reset, queue preview/return, and floating expansion/grid/list switching. Captures are in `build/verification/ui-polish/`; the temporary review source is removed after verification.

Workflow reference: unslop by Damir Sokolovsky (@heydamir), CC BY 4.0.

## Product goals

- Select a static JPEG, PNG or WebP, compress it, compare file sizes, and save a copy or explicitly overwrite the source.
- Support both single-image inspection and sequential batch compression for multi-file and folder selections without inflating idle memory.
- Do not resize images; normalize JPEG EXIF orientation so photos retain their displayed direction. Preserve the source until the user confirms an overwrite.
- Keep the smaller of the original and re-encoded data.
- Non-goals: format conversion, resizing, watermarking, accounts, cloud services, and target-file-size search.
- Success: a first-time user can produce a compressed copy without guessing the next action.

## Personas and jobs

People preparing photographs and screenshots for sharing or uploading. They need an understandable size reduction and a preserved original.

## Information architecture

One main window, one modal child settings window, and an optional floating drop window:

- Main window: product header with a “设置” entry; image/empty preview and file details; scrollable saved compression-scheme choices; result summary; primary compress or save action. No parameter sliders or advanced disclosure.
- Main window: product header with a “设置” entry; empty, single-image preview, or batch queue / item preview on the left; scrollable saved compression-scheme choices, batch output-mode toggle (“保存到文件夹 / 覆盖原图”), result summary, and primary compress/continue/save actions on the right. No parameter sliders or advanced disclosure.
- Settings dialog: “设置” heading and a “压缩方案” section; a scheme list with add/duplicate/delete actions on the left; a scrollable editor for the name, shared JPEG/WebP quality, PNG parameters, and advanced settings on the right; fixed “取消” / “保存” actions and inline feedback.
- The dialog is a parented native modal window (a sheet on macOS). Reopening focuses the existing dialog without resetting edits. Both formats can be configured before selecting an image.
- Save validates and persists the draft list before applying it. Cancel, Escape, and native window close discard edits. The main window remains on screen and resumes after dismissal.
- Floating drop window: opened or closed from the main header. A movable 176 × 184 point compact panel keeps circular close/expand controls, up to three rotated thumbnails, and a centered image-count pill. Clicking the stack, chevron, or count expands the same panel to 480 × 360 points. Expanded mode has a back control, image count and selected size, filter/sort menus, a grid/list switch, image cells, and a Finder reveal cell. “查看队列 / 下载位置” stay in the expanded footer. Both sizes accept local files/folders and browser image transfers. Main-window closure also closes this panel and cancels in-flight work.

## Design principles

- Keep frequent compression actions on the main window and scheme maintenance in the settings dialog.
- Start with built-in “体积优先 / 均衡 / 画质优先”, defaulting to “均衡”. Built-in schemes are read-only and non-deletable; user-added schemes are named, editable, and deletable. Additions become selectable after saving. Restore saved schemes and the selected scheme on startup.
- Each scheme stores a shared JPEG/WebP quality and separate PNG parameters. The existing persisted quality field retains its value and schema; label the shared control “JPEG / WebP 质量”. Editing a draft does not change the active scheme or its compression result until Save succeeds. Names are trimmed, nonempty, unique, free of control characters, and at most 40 Unicode characters.
- JPEG/WebP quality is 1–100. WebP always uses lossy RGB encoding, even at 100, with lossless alpha and supported EXIF/ICC/XMP retention; reject animation by container flags/chunks and never flatten it into one frame. Preserve WebP pixel orientation and supported metadata instead of auto-rotating it. PNG exposes target quality 1–100 and maximum colors 2–256, with minimum quality 0–target and speed 1–11 under “高级设置”. Lowering the target clamps the minimum. Explain that PNG quality is a tool score, not a percentage of quality retained.
- Do not offer a separate PNG lossless compression path. All schemes tune the same lossy pipeline.
- PNG always uses pngquant, then Oxipng to optimize the candidate. Label it “PNG 有损压缩” and explain that colors may change, especially gradients and translucent edges. Accept 8/16-bit inputs without promising to retain channel precision or metadata.
- JPEG orientation is normalized before its metadata is removed.
- Show exact output size after processing; do not predict savings.
- Keep operations responsive and explicitly report errors.

## Visual language

- Follow the desktop's light/dark theme using MyGo's semantic theme colors.
- Use the system font: 24-point window headings, 16-point peer section headings, 13-point body/control text and 12-point metadata. Primary and secondary text use theme roles; metadata uses #707078 in light mode and #92929c in dark mode.
- Spacing: a four-point grid, 8/12-point control gaps and 16/20-point panel padding. Main page edges are 24 points on desktop and 16 below 960 points wide.
- A single blue #2563eb accent for primary actions, selection outlines and keyboard focus. Selected surfaces mix 6% accent in light mode and 12% in dark mode, with a 1-point 60%-opacity accent border and ordinary text colors; focus remains independently visible. Disabled primary actions use neutral surfaces and preserve their disabled semantics.
- Controls use 8-point corners; panels use 16-point corners. Surfaces and hairlines remain neutral.
- Text weights are 400–600. Source/output summary figures use 18/24 points on desktop and 16/20 points below 960 points wide, with a wider output column, tabular digits and status on its own line.
- Images are contained without cropping. Transparent pixels and unused canvas space show the plain theme background.
- No nonessential animation.
- Batch queue items stack vertically as thumbnail rows: a 64-point aspect-fit image with 4-point corners, filename and textual status above dimensions and file sizes, 12-point internal padding, 4-point text gaps and 8-point gaps between rows. Unselected rows share the neutral panel surface; selection uses the shared tint/outline without replacing native focus. Thumbnail failures retain a neutral “无预览” placeholder and the processing error.
- The floating drop window retains native blurred popover material with an opaque theme fallback, 20-point window corners, 8-point outer padding and 4-point section gaps. Its 28-point window close/back controls remain circular; other 28-point action controls use 8-point corners and 16-point icons with 1.5-point strokes. Compact images retain −9°, +7°, and 0° stacking with soft neutral shadows. Expanded grids retain 136-point minimum columns and 128-point rows; lists retain 56-point rows. Image cells and view switches use the same selection tint/outline and normal text colors as the main app. Drag highlighting, the “松开加入队列” count pill, status/error tooltips and acknowledgment/cancel actions are preserved.

## Components

Use MyGo's native modal child window and built-in radio group, segmented control, collapsible, scroll, text input, row, column, buttons, slider, progress indicator, image, and panels. Show saved schemes as keyboard-operable radio choices in a scrollable region. Application state owns the saved scheme list and selection, dialog draft and editing index, selected image, processing status, result, and message. Shared JPEG/WebP quality does not control PNG. Reuse the same theme across both windows; no additional component framework.

Initial settings: size first = JPEG/WebP 60, PNG quality 40–65 / 128 colors / speed 3; balanced = JPEG/WebP 80, PNG 65–85 / 256 / 3; quality first = JPEG/WebP 92, PNG 85–100 / 256 / 1. Save persists the complete scheme list and selected scheme to versioned JSON in the system user configuration directory. Selecting a scheme on the main window also persists that selection.

## Accessibility

Use built-in keyboard-operable widgets, visible focus, descriptive text labels, and semantic theme colors. Show status in text rather than color alone. Make processing and save actions identifiable without icons. Full screen-reader behavior remains subject to platform verification.

On the main window, Command/Ctrl+O selects or adds images, Command/Ctrl+Enter starts/continues compression, Command/Ctrl+S saves a copy (or chooses the batch output folder in folder-export queue mode), and Command/Ctrl+, opens settings. In the batch queue list, Command/Ctrl+F focuses the filename search field, and Delete/Backspace removes the selected item only when text input does not hold focus. In batch preview, Escape returns to the queue and Command/Ctrl+[ / ] steps to the previous or next item in the active filtered and sorted order. When an image preview is active (single-image or batch preview), Command/Ctrl+- zooms out, Command/Ctrl+= (or Command/Ctrl+Shift+=) zooms in, Command/Ctrl+0 resets to fit, and Command/Ctrl+1 zooms to 1:1 (100%). Expose shortcut metadata and visible keycaps; action buttons and the search field also surface their shortcuts in tooltips. Busy operations, overwrite confirmations, and the settings dialog suppress these main-window shortcuts. In settings, Command/Ctrl+S saves the scheme draft and Escape cancels. Radio groups support arrow keys and one Tab stop. Native modality confines interaction to the settings window while it is open.

## Responsive behavior

Desktop-first: the main window defaults to 980 × 720 points, minimum 760 × 660; settings defaults to 820 × 650, minimum 760 × 560. Below 960 points wide the main page uses 16-point edges/gaps and a 280-point control column instead of 24-point edges, a 20-point gap and a 320-point column. Batch preview moves comparison modes onto a separate row in that range, preserving navigation and zoom controls. The main window keeps results/actions stable while scheme choices scroll when space is constrained. Settings keeps its header and Save/Cancel footer fixed; scheme list and parameter editor scroll independently. Advanced parameters remain keyboard reachable at minimum size. Long paths retain truncation and full-value tooltips. This iteration targets native desktop and narrow windows; a mobile website or application remains a separate platform scope.

The floating panel changes size explicitly, keeping its nearer horizontal edge and top position where possible and clamping both states to the current display's work area. Expanded grids adapt their column count and virtualize offscreen cells; list mode also virtualizes. Main and floating windows share the existing 96-entry, 144-pixel thumbnail cache and one decoder, with independent visible-item demand and fair scheduling.

## Interaction states

- Empty: supported formats, privacy note, select-image action. Scheme selection and maintenance are usable before an image is selected.
- Empty: supported formats, privacy note, select-image and select-folder actions, plus drag-and-drop for single files, multi-files, or folders. Scheme selection and maintenance are usable before an image is selected.
- Floating import: every accepted drop enters the batch queue, including single files; preserve an existing single source when promoting it into a queue. While compression, settings, saving, or confirmation blocks import, retain up to 16 pending drop groups and process them serially once free. Closing the floating window retains pending work; closing the main window cancels work and releases queued transfers.
- Floating gallery: read the current single image or batch queue; opening, closing, expanding, or changing views never changes image data or processing results. Keep floating filter/sort independent of main-window filters and preserve selection by source path across sorting, view changes, and queue updates. Grid/list arrows select images; Enter or double-click opens a valid selected image in the main preview. Finder reveal prefers an existing output association, otherwise the source, and respects operation/modal guards. Empty filters offer “显示全部”; a fully empty panel explains drag input. Escape collapses the gallery, then closes the compact panel. Closing releases floating thumbnail demand.
- Web images: prefer browser-provided image sources over enclosing page links, accept HTTP(S) image URLs and supplied PNG/JPEG/WebP bytes, validate static JPEG/PNG/WebP and existing size/pixel limits, and retain downloaded originals in the system Downloads/Z Pic folder with collision-free names. Repeated URLs reuse an existing downloaded original. Show actionable unsupported/network errors, support cancellation, and continue valid items in mixed drops. Compression remains local; the footer explicitly distinguishes web downloading.
- Import errors and pending-queue overflow stay visible across later successful drops until “清除提示”; retain the 16 most recent error groups in the status tooltip. Acknowledging errors preserves every queued image and downloaded file.
- Batch queue: virtualized list (`ui.List`) with status filtering (`全部 / 待处理 / 已完成 / 失败`), filename search (`Command/Ctrl+F` to focus), queue sorting (`队列排序`: `默认顺序` / `按名称` / `原大小 ↓` / `节省率 ↓`, keeping underlying item indices stable and preserving selection across sort/filter changes), add-image, add-folder, clear, remove (button or `Delete` / `Backspace` while not editing text), one-click failure/invalid cleanup (`移除无效项` for `invalidInput` items and `移除失败项` for all failed items, restoring `全部` when removing the last failed items under `失败` filter), retryable failure recovery (`重试失败项`, selected-item `重试`, and preview `重试` / `移除`, keeping `invalidInput` non-retryable and requiring overwrite confirmation when `覆盖原图` is active), per-item output reveal (`定位文件` via `ShowItemInFolder` for saved or overwritten outputs in both queue selection bar and item preview), and filtered/sorted item preview navigation (`预览` / double-click / Enter / `上一张` `Command/Ctrl+[` / `下一张` `Command/Ctrl+]` / `Esc` return). Header-only validation (`compress.Inspect`) during directory scans limits memory; valid items are processed sequentially on one background worker and saved immediately with collision-free naming (`保存到文件夹`, remembering the last existing `batch_output_dir` in `settings.json` when settings are valid) or confirmed atomic overwrite (`覆盖原图`, skipping non-smaller results). Batch summary surfaces actual reduction, saved byte total, and unchanged counts without overflowing `880×660`. Canceling mid-batch preserves completed items and allows continuing remaining pending items or restarting. Switching active scheme parameters or output mode invalidates prior results.
- Selected: image preview, dimensions, original size, scheme selection.
- Settings: opening clones saved schemes into a draft; modifications, additions, and deletions stay in that draft until Save. Deletion requires inline confirmation and cannot remove the last scheme. Cancel, Escape, and native close discard changes. Reopening does not duplicate the dialog or reset an in-progress draft.
- Duplicate scheme: “复制方案” copies the selected draft's shared JPEG/WebP quality and PNG settings into a new editable custom draft, including when the source is built-in. Generate a unique “<name> 副本” name with a numeric suffix on collision, keeping the full name within 40 Unicode characters. Select the copy for editing while retaining the active main-window scheme and compression result. Save publishes the copy; Cancel discards it.
- Compress one batch item: in the preview of a valid pending item, “压缩此张” runs only that item using the active scheme and batch output settings, returning to the queue and retaining selection when the item matches the active filter. Reuse the single-item retry worker path and overwrite confirmation with a target count of one. Completed items, other pending items, and unrelated failures retain their state and files. Busy work, settings, and confirmation dialogs guard the action; failed items continue to offer “重试”.
- Save settings: validate every name and parameter; atomically replace the saved configuration before applying it. Failure keeps the dialog, draft, active schemes, and existing result intact and shows an actionable error. Saving edits to another scheme or renaming the active scheme preserves the current result. Changed active compression parameters invalidate it.
- Startup: load saved schemes and selection. Missing configuration uses independent defaults. Invalid configuration reports an error, uses defaults, and leaves the original file intact until the user explicitly saves settings.
- Main-window file drops and image/compression actions are ignored while settings is open, so the dialog cannot discard an edit or start hidden work.
- Processing: status and cancel action; prevent concurrent work.
- Result: original/output sizes, reduction, save-copy action, and a distinct overwrite-original action.
- Result comparison: “对比 / 原图 / 结果” switches the preview without changing the selected bytes, compression settings, export result, or result bitmap. New results default to a centered draggable split, original on the left and output on the right, with measured file-size labels. Both bitmaps share one destination rectangle and use complementary clips so transparent pixels reveal the plain background without showing the other image. The divider supports pointer capture, arrow keys, Home (all output), and End (all original), with native slider accessibility and visible keyboard focus.
- Preview inspection: zoom buttons, keyboard zoom shortcuts (`Command/Ctrl+-`, `Command/Ctrl+=` / `Command/Ctrl+Shift+=`, `Command/Ctrl+0`, `Command/Ctrl+1`), mouse wheel, and touchpad pinch share a 1%–1600% scale; “1:1” shows the original dimensions and “适应” resets scale and pan to fit. Touchpad scrolling pans both images together; single-image modes also allow dragging to pan. Pinch and wheel zoom retain the image point under the pointer. Mode changes retain the split, scale, and pan. New selections/results and parameter changes restore fit; busy work, settings, and overwrite confirmation disable preview controls and shortcuts.
- Parameter changes restore the source preview. Once the source is overwritten, hide comparison because the previous source is no longer the selected original; keep the output preview and its camera.
- No reduction: show “未减小（0.0%）”, both sizes, and explain that the original data has been retained.
- Error: inline actionable message; retain a valid previous selection where possible.
- Save copy: native save dialog; cancellation is neutral. The copy API continues to reject the source path and existing files. After saving a copy or overwriting, expose `定位文件` (`ShowItemInFolder`) beside the status message when the saved file exists on disk, clearing it on errors, recompression, parameter invalidation, or new selections.
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
- Offline: local import and compression work locally; web-image downloads require connectivity.

## Content voice

Concise Chinese. Use “压缩图片”, “JPEG / WebP 质量”, “另存副本”, and “覆盖原图”. Do not claim that the original is always preserved. Use “PNG 有损压缩” and explain the impact on colors beside its controls. Do not promise a smaller file.

## Implementation constraints

- MyGo native UI, pinned dependency and Go toolchain.
- Settings use Go standard-library JSON and file APIs, with a size limit, validation, a synced same-directory temporary file, and atomic replacement. Failed writes preserve the previous file and clean up temporary files. Newly created configuration directories and files are private to the user.
- Standard-library JPEG encoding and PNG validation; bundled Oxipng and pngquant CLI tools for PNG. WebP decoding reuses the existing pinned golang.org/x/image/webp module, with a bundled static libwebp/cwebp 1.6.0 encoder for darwin/arm64. No new Go modules or versions.
- Native tools operate on an immutable byte snapshot through stdin/stdout, never the source path. Executables are bundled for the verified target, with pinned versions and third-party notices.
- Maximum input: 64 MiB and 32 million pixels.
- Background processing; serialize image operations to limit memory use.
- Queue thumbnails are loaded only for virtualized rows and their overscan, with one thumbnail decoder at a time, 144-pixel maximum raster edge, and an LRU bound of 96 entries. Keep only the small raster, discard stale results after queue changes or source overwrites, and cancel thumbnail work on queue replacement or application close. Full-resolution decoding remains subject to the existing input limits.
- Browser drops use MyGo's native `transfer` representations and `ui.DropData`, never a scraping web view. Downloads use bounded, cancelable HTTP requests and validate bytes before publishing a private file; no page scraping, browser authentication reuse, or new image formats.
- Check cancellation before and after codecs; cancel external PNG/WebP processes with their context. WebP uses bounded stdin/stdout with a two-minute encoder cap and validates container structure, static status, dimensions and decoded output before accepting it. Keep the source if output is no smaller or a valid input exceeds the encoder's 16383-pixel edge limit. Standard JPEG encoding does not guarantee immediate interruption.
- Unit tests must cover file preservation, format behavior, invalid input, and cancellation. Build and native-window smoke checks are required before claiming runtime readiness.
- Overwrite tests must cover confirmation/cancellation, source-change detection, write failure, temporary-file cleanup, permissions, and the updated UI snapshot. The supported macOS path uses atomic same-directory rename; the final source check and rename are separate operations and do not constitute a cross-process compare-and-swap.

## Open questions

- [ ] Owner: user. Which operating systems should receive release installers? The first build and runtime check target the available macOS machine.
- [ ] Owner: user. Is batch processing needed next? It is excluded from this first release.
- [x] Batch processing implemented: multi-file/folder/CLI/drag-drop entry, sequential execution, collision-free folder export and confirmed atomic overwrite, mid-batch cancel/resume, and single-item preview.
