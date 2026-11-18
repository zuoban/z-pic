# Design

## Source of truth

- Status: Active
- Date: 2026-11-17
- Surface: Z Pic main compression window, native settings dialog, and an optional always-on-top image drop window, built with MyGo native UI.
- Evidence: existing MyGo application, native-window screenshots, image compression tools, behavior tests, and the supplied macOS floating image-stack/gallery references. Settings maintain saved compression schemes in a separate popup; the current floating-panel refinement follows the two reference states while preserving image import and compression behavior.
- Defaults: single-image or batch static JPEG, PNG and WebP, Chinese interface, local processing.

## Brand

Quiet, precise, and practical. Show actual file sizes and explicit processing results. Avoid decorative gradients, emoji, fabricated savings, and unrelated tools.

The application icon is `resources/icon.png`, a 1024 × 1024 PNG with transparent outer margins. A cobalt rounded-square tile carries a white photograph symbol whose negative space joins a Z-shaped diagonal with a landscape and sun. Restrained material lighting belongs to the native app icon; interface surfaces retain their neutral treatment. `mygo.json` explicitly selects this asset, and MyGo generates the platform icon formats when packaging.

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

- Main window: hides the redundant native title bar (`TitleBarHiddenInset`) so the unified top action bar serves as the draggable window header while keeping clear of native window controls (`c.TitleBar()`). Image preview or batch queue sits on the left; a top-aligned compression overview on the right shows the active preset and quality level, measured results, and save destination. The information area scrolls while status, save actions, output-folder access and cancellation remain fixed at the bottom. The top action bar contains start/continue/recompress actions, floating-window controls and Settings, shared by empty, single-image, batch-list and batch-preview views. Appearance and preset selection live only in Settings. Whole-batch compression actions are disabled during batch preview; per-image compression remains in the preview.
- Settings dialog: a top category control switches between General (appearance and shortcut reference) and Presets. A fixed “当前预设” picker beneath the categories is shared by both pages; it lists draft presets, marks the pending active choice, and selects the chosen preset for editing without applying it before Save. “管理预设” opens the pending active preset. Presets retains a preset tab strip with an add button and a single scrollable column for the editable name, image-quality card, destination and filename card. “设为当前预设” sits beside the name for an inactive draft. Image quality controls JPEG/WebP quality and all PNG quantization options together; individual advanced controls are not exposed. Cancel and Save remain in the shared fixed footer; duplicate and delete appear only on Presets. Pending selection changes are named in the footer, and selection is blocked during deletion confirmation.
- The dialog is a parented native modal window (a sheet on macOS). Reopening focuses the existing dialog without resetting edits. Presets can be configured before selecting an image.
- Save validates and persists the draft list before applying it. Cancel, Escape, and native window close discard edits. Editing another tab does not change the active preset unless “设为当前预设” is chosen.
- Floating drop window: opened or closed from the main header. A movable 176 × 184 point compact panel keeps circular close/expand controls, up to three rotated thumbnails, and a centered image-count pill. Clicking the stack, chevron, or count expands the same panel to 480 × 360 points. Expanded mode has a back control, image count and selected size, filter/sort menus, a grid/list switch, image cells, and a Finder reveal cell. “查看队列 / 下载位置” stay in the expanded footer. Both sizes accept local files/folders and browser image transfers. Main-window closure also closes this panel and cancels in-flight work.

## Design principles

- Use the supplied preset reference: named tabs with an active check, a six-stop image-quality slider, and grouped save options.
- Initialize a single “均衡” default preset when no configuration exists. All presets are editable and deletable, with at least one retained. Restore existing names, values, output settings and selection without pruning saved presets or recreating deleted ones.
- Each preset stores shared JPEG/WebP quality, PNG parameters, and output preferences. The six stops map to JPEG/WebP quality 40/60/70/80/92/100 plus corresponding PNG quantization settings; stop 3 is “均衡质量”. Selecting a stop updates all compression parameters together. Existing custom values are preserved and show “自定义质量” until another stop is chosen. Names are trimmed, nonempty, unique, free of control characters, and at most 40 Unicode characters.
- Image quality is the only compression control. WebP retains lossy RGB encoding even at 100, lossless alpha, and supported metadata. PNG quality range, color cap and speed follow the selected stop automatically. PNG quality is a tool score, not a percentage of retained detail.
- Output destinations are “原文件夹 / 每次询问 / 固定输出目录”. The optional subfolder appears and applies for original-folder and fixed-directory destinations; its enabled state and name persist when switching destinations, and ask mode ignores it. The suffix is available for every destination. Subfolder and suffix default to `zpic` and `-zpic`; the suffix is appended directly before the extension, as in `example-zpic.png`. Original-folder output with a subfolder writes a copy inside each source file’s own directory, including when no suffix is enabled. Original-folder output without either modifier requests the existing overwrite confirmation. Copy output avoids existing files; fixed/asked directories retain imported relative paths. Ask on every batch run, including resume/retry, with the last usable directory only as the picker's starting point.
- Do not offer a separate PNG lossless compression path. All schemes tune the same lossy pipeline.
- PNG always uses pngquant, then Oxipng to optimize the candidate. Document that colors may change, especially gradients and translucent edges. Accept 8/16-bit inputs without promising to retain channel precision or metadata.
- JPEG orientation is normalized before its metadata is removed.
- Show exact output size after processing; do not predict savings.
- Keep operations responsive and explicitly report errors.

## Visual language

- Follow the desktop's light/dark theme by default (`跟随系统`), with `浅色` and `深色` choices in Settings → General. Preview immediately across native window chrome (`mygo.Theme.SetSource`), main window, settings dialog, and floating drop window; persist in `settings.json` on Save and restore the previous theme on Cancel.
- Use the system font: 24-point window headings, 16-point peer section headings (600 in the main workspace), 13-point body/control text and 12-point metadata. Main workspace headings share a 32-point header row. Primary and secondary text use theme roles; metadata uses #626875 in light mode and #969ba8 in dark mode. High contrast promotes metadata to the stronger secondary-text role.
- Spacing: a four-point grid, 8/12-point control gaps and 16/20-point panel padding. Main page edges are 24 points on desktop and 16 below 960 points wide.
- Keep blue #2563eb for primary-action fills, with #1d4ed8 hover and #1e40af pressed states so white labels retain contrast. Blue text and small icons use #93b4ff in dark mode; selection outlines and keyboard focus follow that readable foreground. Selected opaque surfaces mix 7% accent in light mode and 14% in dark mode and retain ordinary text colors. Disabled primary actions use neutral surfaces and preserve their disabled semantics.
- Controls use 8-point corners and opaque panels use 12-point corners. Native glass uses 16-point panels, pill-shaped action buttons and 12-point input corners; input fields retain their field shape rather than becoming pills. Action buttons and fields have stronger neutral outlines than panel hairlines, with distinct hover and pressed feedback.
- Dark surfaces progress from #141416 window chrome to #1e2026 panels, with #181a1f inset summaries and #101216 preview stages. Light panels remain white over #f7f7f8 chrome. Success, warning and error text have separate appearance-specific colors and retain textual status labels.
- Native glass keeps transparent window roots and translucent panels. Either Reduce Transparency or Increase Contrast uses opaque theme surfaces across the main window, settings and floating panel. Apply accessibility preferences after the app palette so stronger borders, opaque keyboard focus and the default font's text scale survive theme changes; retain the product accent.
- Settings use the pinned MyGo Liquid Glass material: native frosted section panels, refractive selected segments and pill controls, subtle blue-tinted selections and a blue glass Save action. Input fields and shortcut keycaps retain quiet inset surfaces. Glass supplies its own rim, without an extra border or shadow; Reduce Transparency and Increase Contrast restore the opaque controls, and Reduce Motion removes the glass press animation.
- Text weights are 400–600. Source/output summary figures use 18/24 points on desktop and 16/20 points below 960 points wide, with a wider output column, tabular digits and status on its own line.
- Images are contained without cropping. Transparent pixels and unused canvas space show the plain theme background.
- No nonessential animation.
- Passive shortcut keycaps and disabled primary actions use an opaque neutral surface derived from the current panel and text colors. Keycaps share one component across the main footer and Settings. Zoom buttons and the percentage readout share one outlined group; keyboard focus remains native.
- Batch queue items stack vertically as thumbnail rows: a 64-point aspect-fit image with 4-point corners, filename and textual status above dimensions and file sizes, 12-point internal padding, 4-point text gaps and 8-point gaps between rows. Unselected rows share the neutral panel surface; selection uses the shared tint/outline without replacing native focus. Thumbnail failures retain a neutral “无预览” placeholder and the processing error.
- The floating drop window retains native blurred popover material with an opaque theme fallback, 20-point window corners, 8-point outer padding and 4-point section gaps. Its 28-point window close/back/expand controls remain circular; other 28-point action controls use 8-point corners and 16-point icons with 1.5-point strokes. Compact images retain −9°, +7°, and 0° stacking with soft neutral shadows. Expanded grids retain 136-point minimum columns and 128-point rows, with a 48-point thumbnail region and two lines for complete sizes or dimensions; lists retain 56-point rows. Image cells and view switches use the same selection tint/outline and normal text colors as the main app; failed-file metadata uses the danger role with a textual status. Drag highlighting, the “松开加入队列” count pill, status/error tooltips and acknowledgment/cancel actions are preserved.

## Components

Use MyGo's native modal window, keyboard-operable horizontal preset choices, segmented destination control, collapsible, scroll, text input, buttons and sliders. Reuse the existing semantic light/dark theme. Preset settings use Liquid Glass panels with opaque accessibility fallbacks, neutral chrome, blue selected segments and active checks, a white slider thumb, and one combined filename example below the naming fields.

The settings dialog separates “通用” and “预设” with a keyboard-operable category control. The settings toolbar button and Cmd/Ctrl+, enter General; “管理预设” opens Presets for the pending active choice. General contains the existing system/light/dark appearance preference and shortcut reference. Preset maintenance actions appear only on Presets. The category control and shared preset picker are disabled during deletion confirmation. Preset names use a visible “预设名称” label above a 36-point outlined input with 12-point horizontal padding and the shared field surface; the active indicator or activation action stays beside the input.

The shared preset picker uses a bounded, left-aligned 36-point field with a trailing chevron, middle-truncated names and a full-name tooltip. Its opaque menu retains radio selection semantics, matches at least the field width (up to 360 points), and uses 12-point corners and a restrained downward shadow. Appearance uses three equal 64-point choices with monitor, sun and moon outline icons. The selected choice shares the app's accent tint and outline, with an explicit check beside its label; native radio-group semantics, arrow-key selection and focus remain intact.

Settings uses a continuous top surface for its 56-point category row and 56-point preset row, with 24-point horizontal alignment and one divider beneath. Category and destination controls share the same 36-point inset segmented treatment. General opens directly on the appearance and shortcut groups, with 20-point spacing between groups and compact 28-point shortcut rows. Preset tabs sit within the body margins, share the app's accent tint for selection, and use a plain check for the active preset. The default footer is one 36-point action row within 12-point vertical padding; preset maintenance uses quiet text buttons. Errors and pending active-preset changes add a status line above the actions.

Settings honor the system text scale across headings, metadata, keycaps, fields and actions, increasing control heights on the four-point grid. Navigation, body and footer share 16-point page margins below 560 points. The content scrolls independently of the fixed navigation and actions. Preset tabs use native tab semantics and Left/Right/Home/End navigation; selection changes reveal the tab without continually overriding manual scrolling. Add and Duplicate select the suggested name for immediate replacement. The name field shows a Unicode character count and associated inline validation; empty drafts retain an “未命名预设” label.

Validation failures reveal the offending preset and focus its name, quality or naming control while keeping the live configuration untouched. Deletion confirmation locks the editing controls, navigation and maintenance methods, focuses “保留预设”, and keeps the complete target name in its tooltip. Escape first dismisses this confirmation; a second Escape closes and discards settings. Ordinary draft changes show “有未保存的更改” in the existing action row at native window widths, keeping the buttons stationary. Output examples combine only the options enabled for the selected destination, while the overwrite warning stays above the destination control.

The application owns saved presets, active selection, output preferences and one cloned dialog draft containing appearance and preset edits. Switching categories preserves both drafts. Shared Save atomically persists and applies both pages; Cancel, Escape and window closure discard both. Appearance changes in settings immediately preview across all windows and native chrome without changing the committed preference. Save persists the choice; Cancel, Escape and window closure restore the committed appearance. Write failures retain the draft and its appearance preview while preserving committed settings; invalid preset values reveal Presets with the validation error.

The main window only displays the current preset. Preset selection, quality, destination, naming and maintenance are edited only in the modal draft and apply after Save succeeds. Selecting another preset also selects it for editing; browsing an editing tab alone preserves the pending active choice. Version 1 JSON accepts optional output and theme fields, preserving older configuration values and atomic writes. The retired favorite field remains accepted for compatibility, is ignored on load and omitted on save. Theme-only edits preserve compression results. Output-only edits preserve a single encoded result and reset batch results only when the preferences actually change.

## Accessibility

Use built-in keyboard-operable widgets, visible focus, descriptive text labels, and semantic theme colors. Show status in text rather than color alone. Make processing and save actions identifiable without icons. Full screen-reader behavior remains subject to platform verification.

On the main window, Command/Ctrl+O selects images, Command/Ctrl+Enter starts or continues compression, Command/Ctrl+S saves the single result according to its preset (or opens the batch destination picker in ask mode), and Command/Ctrl+, opens settings. Existing batch search, removal, preview navigation and image zoom shortcuts remain available. Modal dialogs and background work guard main-window actions.

## Responsive behavior

Desktop-first: the main window defaults to 980 × 720 points, minimum 760 × 660; preset settings defaults to 724 × 700, minimum 640 × 600. Below 960 points wide the main page uses 16-point edges/gaps and a 280-point control column. The top preset button bounds long names, and its popover bounds label width and scrolls long lists. Settings preset tabs scroll horizontally, card content scrolls vertically, and actions stay fixed. Settings keeps the tab strip and footer fixed while the name, quality and save options scroll. Long names and paths truncate with full-value tooltips.

The floating panel changes size explicitly, keeping its nearer horizontal edge and top position where possible and clamping both states to the current display's work area. Expanded grids adapt their column count and virtualize offscreen cells; list mode also virtualizes. Main and floating windows share the existing 96-entry, 144-pixel thumbnail cache and one decoder, with independent visible-item demand and fair scheduling.

Below 1100 points on the two-column desktop workspace, batch failure banners keep their message above a grouped action row, and selected-file details sit above the file actions. Below 960 points, queue metadata and size/error details occupy separate lines. Queue cards use 12-point padding and 8-point row gaps. Preset save descriptions occupy their own bounded line with a full-value tooltip so long directories do not compete with the section title.

## Interaction states

- Empty: supported formats, privacy note, select-image action. Scheme selection and maintenance are usable before an image is selected.
- Empty: supported formats, privacy note, select-image and select-folder actions, plus drag-and-drop for single files, multi-files, or folders. Scheme selection and maintenance are usable before an image is selected.
- Floating import: every accepted drop enters the batch queue, including single files; preserve an existing single source when promoting it into a queue. While compression, settings, saving, or confirmation blocks import, retain up to 16 pending drop groups and process them serially once free. Closing the floating window retains pending work; closing the main window cancels work and releases queued transfers.
- Floating gallery: read the current single image or batch queue; opening, closing, expanding, or changing views never changes image data or processing results. Keep floating filter/sort independent of main-window filters and preserve selection by source path across sorting, view changes, and queue updates. Grid/list arrows select images; Enter or double-click opens a valid selected image in the main preview. Finder reveal prefers an existing output association, otherwise the source, and respects operation/modal guards. Empty filters offer “显示全部”; a fully empty panel explains drag input. Escape collapses the gallery, then closes the compact panel. Closing releases floating thumbnail demand.
- Web images: prefer browser-provided image sources over enclosing page links, accept HTTP(S) image URLs and supplied PNG/JPEG/WebP bytes, validate static JPEG/PNG/WebP and existing size/pixel limits, and retain downloaded originals in the system Downloads/Z Pic folder with collision-free names. Repeated URLs reuse an existing downloaded original. Show actionable unsupported/network errors, support cancellation, and continue valid items in mixed drops. Compression remains local; the footer explicitly distinguishes web downloading.
- Import errors and pending-queue overflow stay visible across later successful drops until “清除提示”; retain the 16 most recent error groups in the status tooltip. Acknowledging errors preserves every queued image and downloaded file.
- Batch queue: virtualized list (`ui.List`) with status filtering (`全部 / 待处理 / 已完成 / 失败`), filename search (`Command/Ctrl+F` to focus), queue sorting (`队列排序`: `默认顺序` / `按名称` / `原大小 ↓` / `节省率 ↓`, keeping underlying item indices stable and preserving selection across sort/filter changes), add-image, add-folder, clear, remove (button or `Delete` / `Backspace` while not editing text), one-click failure/invalid cleanup (`移除无效项` for `invalidInput` items and `移除失败项` for all failed items, restoring `全部` when removing the last failed items under `失败` filter), retryable failure recovery (`重试失败项`, selected-item `重试`, and preview `重试` / `移除`, keeping `invalidInput` non-retryable and requiring overwrite confirmation when `覆盖原图` is active), per-item output reveal (`定位文件` via `ShowItemInFolder` for saved or overwritten outputs in both queue selection bar and item preview), and filtered/sorted item preview navigation (`预览` / double-click / Enter / `上一张` `Command/Ctrl+[` / `下一张` `Command/Ctrl+]` / `Esc` return). Header-only validation (`compress.Inspect`) during directory scans limits memory; valid items are compressed concurrently through a bounded worker pool and retired in queue order with immediate collision-free naming (`保存到文件夹`, remembering the last existing `batch_output_dir` in `settings.json` when settings are valid) or confirmed atomic overwrite (`覆盖原图`, skipping non-smaller results). Batch summary surfaces actual reduction, saved byte total, and unchanged counts without overflowing `880×660`. Canceling mid-batch preserves completed items and allows continuing remaining pending items or restarting. Switching active scheme parameters or output mode invalidates prior results.
- Selected: image preview, dimensions, original size, scheme selection.
- Settings: opening clones saved schemes into a draft; modifications, additions, and deletions stay in that draft until Save. Deletion requires inline confirmation and cannot remove the last scheme. Cancel, Escape, and native close discard changes. Reopening does not duplicate the dialog or reset an in-progress draft.
- Duplicate preset: copy the selected draft's JPEG/WebP quality, PNG parameters and output preferences into an independent editable draft. Generate a unique “<name> 副本” name within 40 Unicode characters; select the copy for editing and retain the active preset until explicitly changed. Save publishes it; Cancel discards it.
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
- Save result: use the active preset's destination and naming preferences. The original-folder/no-modifier path requests overwrite confirmation; copy paths use collision-free names. “另存副本” remains an explicit native Save dialog and rejects source/existing paths. Cancellation is neutral. Saved results expose `定位文件` via `ShowItemInFolder`.
- Copy result: “复制图片” sits beside “另存副本” for a single result and in the summary actions for a completed batch preview. Copy the result snapshot as a native PNG image, preserving display orientation, dimensions and transparency; disk exports retain their original format. Prepare and validate PNG on a worker, publish on the main thread, report success or errors inline and in a four-second toast, and preserve files, compression results and save state. Repeated copies refresh the same toast; cancellation does not show a success or failure toast. Busy work, settings, overwrite confirmation and shutdown block copying; cancellation or closure before publication leaves the clipboard untouched. Batch previews retain only the currently loaded result bytes, releasing them on leaving the preview.
- Paste input: the toolbar's “粘贴图片” action and Cmd/Ctrl+V read native image and file representations into the existing import pipeline. Use an icon with an accessible label and tooltip on narrow desktop widths. Pasting creates or appends to a batch queue, retaining a selected source and completed batch results; repeated file paths are deduplicated. Raw images are validated and saved unchanged under Downloads/Z Pic before queueing. Errors, duplicates and completed imports receive a four-second toast. Disable import during busy work, settings, overwrite confirmation, pending drops and shutdown; focused text fields keep their normal paste behavior.
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

Concise Chinese. Use “压缩图片”, “图片质量”, “另存副本”, and “覆盖原图”. The quality tooltip explains that compression parameters follow image quality automatically and that lower quality usually produces smaller files. Do not claim that the original is always preserved or promise a smaller file.

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

## Theme refinement verification · 2026-11-17

The Go suite, Go vet, MyGo vet and native automation build passed. New regression checks cover 4.5:1 text contrast across opaque window, panel, preview, inset and selected surfaces, primary-action labels in all three pointer states, and accessibility preferences across system/light/dark overrides without accumulating text scaling. Light/dark renders cover empty, batch, settings, floating and 760-point layouts. Native smoke checks cover switching light → dark → light, settings cancellation and floating compact/expanded states, with captures under `build/verification/theme-native/`. Native captures preserve alpha; composited review images use the corresponding opaque window color to inspect glass layers without a desktop backdrop.

## Impeccable ten-round refinement · 2026-11-18

This pass refines the existing native desktop interface using the Impeccable polish workflow. Compression, imports, save rules, preset persistence and the established graphite/blue visual language are retained.

| Round | Evidence and refinement |
| --- | --- |
| 1 · Shared controls | Native dark captures showed overly bright passive keycaps and disabled actions. Added opaque neutral surfaces and a shared keycap component; toolbar styling now receives disabled state before resolving pointer feedback. |
| 2 · Workspace rhythm | Aligned preview and overview headings on matching 32-point rows, standardized main section weight, retained 8-point top spacing at narrow desktop widths, and corrected the save shortcut label to match its action. |
| 3 · Empty state | Grouped the headline and input guidance, then grouped import actions with format/limit details, creating clear 8/12/20-point spacing without changing the available entry paths. |
| 4 · Preview controls | Combined zoom buttons and tabular percentage into one quiet control group and exposed it as a named native toolbar. |
| 5 · Measured results | Matched figures to the documented 18/24-point scale (16/20 at narrow widths), exposed result status semantically, and explained the output/original ratio in the bar tooltip. |
| 6 · Batch queue | A new regression reproduced the retry button extending into the overview at 760 points. Adapted the failure and selected-file action layouts, separated narrow metadata from sizes/errors, and restored documented card padding/gaps. |
| 7 · General settings | Unified category-control treatment, shortcut keycaps and row dividers; bounded shortcut labels and gave the menu one soft elevation treatment. |
| 8 · Preset editor | Aligned section typography, bounded long tab names, moved save paths to their own line, retained destructive-save warning color and standardized naming-field padding. |
| 9 · Floating panel | Applied circular window controls and 8-point action controls consistently, added pressed feedback, clarified the view switch and made failure metadata explicit. |
| 10 · Render review | Grouped narrow failure actions as one unit, corrected long-tab sizing after capture, and gave grid sizes explicit lines so numbers stay with their units. Added native-chrome, minimum-width, heading-alignment and keycap-contrast regression coverage. |

Repeatable captures are exported by `ZPIC_STYLE_CAPTURES=build/verification/style-10/after go test . -run TestStyleReviewRenders -count=1`. The review uses repository image assets as fixtures, with empty/result/unchanged/error/busy/batch/failure scenes at 760×660, 880×660, 980×720 and 1440×900 in both appearances, minimum/default settings, high contrast and reduced transparency, and compact/grid/list floating states. Screenshots and native smoke evidence are under `build/verification/style-10/`; the opt-in exporter writes no artifacts during ordinary tests. Native screenshots preserve the window's transparent background.

Final verification: `go test -race -json ./...` passed 1,356 tests and subtests across six tested packages; `go vet ./...`, `go tool mygo vet`, `go build ./...`, the native `e2e` build and `git diff --check` passed. The opt-in review exported 72 frames. Native smoke checks compressed an isolated JPEG fixture from 76.1 KiB to 49.7 KiB (34.7%), exercised zoom/fit, previewed the light appearance, navigated to Presets, canceled settings while retaining the measured result, and expanded/switched/collapsed the floating panel. `verification-summary.json` records the final counts and result-preservation check. Full screen-reader behavior and other operating systems remain outside this macOS verification.

## Settings ten-round refinement · 2026-11-18

This settings-specific pass follows the requested Liquid Glass direction and is separate from the earlier whole-app pass. The native glass panels, refractive controls, quiet inset fields and blue Save action remain consistent with the main window. Compression values, draft application and atomic persistence retain their existing rules.

| Round | Finding, change and evidence |
| --- | --- |
| 1 · Hierarchy and semantics | The title and section names were plain text, and the active marker repeated its adjacent label. Added heading levels, a complete current value on the picker, and a decorative check with a meaningful active-tab description. Verified the accessible heading and picker values. |
| 2 · Preset navigation | A selected preset could begin outside the horizontal viewport, and the ad hoc buttons lacked tab navigation. Switched to native tabs with Left/Right/Home/End and reveal on selection changes. A 19-preset fixture verifies first/last navigation without changing the pending active preset. |
| 3 · New and copied names | Adding or duplicating required another selection step before renaming. The new name now receives focus and selects its suggested text. Regression and native typing checks verify immediate replacement without publishing the draft. |
| 4 · Name feedback | Invalid names were reported only at Save and an empty tab could become invisible. Added a Unicode count, immediate associated errors, normalization-aware uniqueness checks and “未命名预设”. Verified empty, whitespace, duplicate, 41-character and control-character drafts and recovery. |
| 5 · Error recovery | Saving from General could report a hidden invalid preset while displaying another valid name. Authoritative validation now returns the preset and field identity; Save reveals that draft and focuses the correction. Verified names, suffixes, subfolders and quality while retaining committed settings. |
| 6 · Deletion safety | Tabs and editor controls remained mutable while a delete target was awaiting confirmation. Locked UI and model operations, defaulted focus to Keep, bounded long-name copy with a complete tooltip, and made Escape dismiss the confirmation first. Verified keyboard behavior and disabled states in tests and the native window. |
| 7 · Naming clarity | Separate examples showed partial names even when their options were disabled. Aligned each checkbox with its field and show one combined example using the selected destination's actual rules. Fixed paths appear once, empty fixed destinations explain the needed choice, and the overwrite warning remains at the top. Verified original, ask and fixed modes. |
| 8 · Quality meaning | The slider exposed only a numeric index and relied on a pointer-only information tooltip. Added the human-readable level, JPEG/WebP and PNG parameter description, custom-value guidance and native keyboard hints. Verified arrows and endpoints update only the draft. |
| 9 · Draft status | Quality and output edits had no shared visible unsaved state. Compare the actual draft with committed settings and show a status that clears when changes are reverted. Retained selection-specific and failure messages; ordinary editing keeps the native footer geometry stable. |
| 10 · Scale and render review | Large-text captures mixed small explicit labels with enlarged default button text. Scaled all settings typography and control heights together, centered the slider drawing and aligned narrow margins. Verified fixed navigation/actions, scrollable content, both appearances, long names, validation, deletion, busy, high contrast and reduced transparency. |

Regression coverage is in `settings_ux_test.go`. The opt-in settings exporter runs with `ZPIC_SETTINGS_CAPTURES=build/verification/settings-10/after go test . -run '^TestSettingsReviewRenders$' -count=1`. It records 60 PNGs with matching accessibility manifests at 640×600 and 724×700 in both appearances, including scrolled naming and large-text variants. The baseline contains 52 frames; the final eight additional frames cover content below the initial viewport. Captures are composited over each appearance's window background to review the non-native material fallback; native smoke screenshots preserve transparency.

Verification: `go test ./...` passed 1,385 tests and subtests across eight packages. `go vet ./...`, `go tool mygo vet`, `go build ./...` and the native `e2e` build passed. Native smoke checks exercised immediate copy renaming, deletion locks and Keep/Enter/Escape behavior, inline validation and failed-save recovery, tab Home/End, quality adjustment, combined filename previews, persisted selection/quality/output/theme, and cancellation restoring the saved theme. Evidence is under `build/verification/settings-10/`; the preview uses an isolated configuration.
