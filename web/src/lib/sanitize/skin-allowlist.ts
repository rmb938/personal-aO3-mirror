// otwarchive's CSS allowlist, the one AO3 cleans work skins with (docs/part4-ui.md, "Security
// model"). Read from otwcode/otwarchive at commit 452391b648f2fb06b55c36133812449747634504
// (2026-11-01): config/config.yml (SUPPORTED_CSS_PROPERTIES and
// SUPPORTED_CSS_SHORTHAND_PROPERTIES), lib/css_cleaner.rb (legal_property?,
// legal_shorthand_property?) and app/models/work_skin.rb (no custom properties, no var(), no
// `position: fixed`). Re-read those files when bumping the commit.
//
// Not ported: css_cleaner.rb's value regexes. They check values for a server-side parser; here
// the browser's own parser reads the skin, so a value is whatever the browser will apply, and
// skin.ts checks that form instead (allowedFunction below).

/** Properties AO3 allows by exact name (SUPPORTED_CSS_PROPERTIES). */
export const cssProperties: readonly string[] = [
	'-replace', '-use-link-source', 'accelerator', 'accent-color', 'align-content', 'align-items',
	'align-self', 'alignment-adjust', 'alignment-baseline', 'appearance', 'aspect-ratio', 'azimuth',
	'baseline-shift', 'behavior', 'binding', 'bookmark-label', 'bookmark-level', 'bookmark-target',
	'bottom', 'box-align', 'box-direction', 'box-flex', 'box-flex-group', 'box-lines', 'box-orient',
	'box-pack', 'box-shadow', 'box-sizing', 'caption-side', 'clear', 'clip', 'color',
	'color-profile', 'color-scheme', 'content', 'counter-increment', 'counter-reset', 'crop', 'cue',
	'cue-after', 'cue-before', 'cursor', 'direction', 'display', 'dominant-baseline',
	'drop-initial-after-adjust', 'drop-initial-after-align', 'drop-initial-before-adjust',
	'drop-initial-before-align', 'drop-initial-size', 'drop-initial-value', 'elevation',
	'empty-cells', 'fill', 'filter', 'fit', 'fit-position', 'float', 'float-offset', 'font',
	'font-effect', 'font-emphasize', 'font-emphasize-position', 'font-emphasize-style',
	'font-family', 'font-size', 'font-size-adjust', 'font-smooth', 'font-stretch', 'font-style',
	'font-variant', 'font-weight', 'grid-columns', 'grid-rows', 'hanging-punctuation', 'height',
	'hyphenate-after', 'hyphenate-before', 'hyphenate-character', 'hyphenate-lines',
	'hyphenate-resource', 'hyphens', 'icon', 'image-orientation', 'image-resolution', 'ime-mode',
	'include-source', 'inline-box-align', 'justify-content', 'layout-flow', 'left',
	'letter-spacing', 'line-break', 'line-height', 'line-stacking', 'line-stacking-ruby',
	'line-stacking-shift', 'line-stacking-strategy', 'mark', 'mark-after', 'mark-before', 'marks',
	'marquee-direction', 'marquee-play-count', 'marquee-speed', 'marquee-style', 'max-height',
	'max-width', 'min-height', 'min-width', 'move-to', 'nav-down', 'nav-index', 'nav-left',
	'nav-right', 'nav-up', 'opacity', 'order', 'orphans', 'page', 'page-policy', 'phonemes', 'pitch',
	'pitch-range', 'play-during', 'position', 'presentation-level', 'punctuation-trim', 'quotes',
	'rendering-intent', 'resize', 'rest', 'rest-after', 'rest-before', 'richness', 'right',
	'rotation', 'rotation-point', 'ruby-align', 'ruby-overhang', 'ruby-position', 'ruby-span',
	'size', 'speak', 'speak-header', 'speak-numeral', 'speak-punctuation', 'speech-rate', 'stress',
	'string-set', 'stroke', 'stroke-width', 'tab-side', 'table-layout', 'target', 'target-name',
	'target-new', 'target-position', 'top', 'unicode-bibi', 'unicode-bidi', 'user-select',
	'vertical-align', 'visibility', 'voice-balance', 'voice-duration', 'voice-family',
	'voice-pitch', 'voice-pitch-range', 'voice-rate', 'voice-stress', 'voice-volume', 'volume',
	'white-space', 'white-space-collapse', 'widows', 'width', 'word-break', 'word-spacing',
	'word-wrap', 'writing-mode', 'z-index'
];

/**
 * AO3's shorthand names (SUPPORTED_CSS_SHORTHAND_PROPERTIES). legal_shorthand_property? matches
 * them anywhere in a property's name, so they allow every longhand too (`border-top-color`,
 * `margin-left`, `text-decoration-line`), which is the form skin.ts sees: the browser expands
 * each shorthand into its longhands.
 */
export const cssShorthands: readonly string[] = [
	'background', 'border', 'column', 'cue', 'flex', 'font', 'layer-background', 'layout-grid',
	'list-style', 'margin', 'marker', 'outline', 'overflow', 'padding', 'page-break', 'pause',
	'scrollbar', 'text', 'transform', 'transition'
];

/** Vendor prefixes legal_property? accepts in front of a listed property (PREFIX_REGEX). */
export const vendorPrefixes: readonly string[] = ['moz', 'ms', 'o', 'webkit'];
