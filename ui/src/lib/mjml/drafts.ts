import type { Part } from './compile';

/**
 * One MJML part's editor state. `saved` is the source on disk (null: none,
 * auth uses Boson's built-in); `base` is what the editor started from
 * (the saved source, else Boson's default); `draft` is what's typed.
 */
export type PartDraft = {
	saved: string | null;
	base: string;
	draft: string;
};

export type Drafts = Record<Part, PartDraft | undefined>;

/** Unsaved: the text differs from what it started from. */
export const isDirty = (d: PartDraft | undefined) => !!d && d.draft !== d.base;

/** A freshly loaded part: saved source, or the default when nothing's saved. */
export function loaded(saved: string | null, fallback: string): PartDraft {
	const base = saved ?? fallback;
	return { saved, base, draft: base };
}

/** After a save: the draft is the new saved source. */
export const afterSave = (d: PartDraft, source: string): PartDraft => ({
	saved: source,
	base: source,
	draft: d.draft === source ? source : d.draft
});

/** After removing the override: back to the default, unsaved edits dropped. */
export const afterRemove = (fallback: string): PartDraft => loaded(null, fallback);

/** "Reset to default" puts Boson's source in the editor, unsaved. */
export const resetTo = (d: PartDraft, source: string): PartDraft => ({ ...d, draft: source });

/** Discard: back to what the editor started from. */
export const discard = (d: PartDraft): PartDraft => ({ ...d, draft: d.base });

/** Per-email editor: Text (subject + message) or the email's own MJML. */
export type Mode = 'text' | 'mjml';

/** The mode an email opens in: MJML when it has its own saved. */
export const initialMode = (hasOverride: boolean): Mode => (hasOverride ? 'mjml' : 'text');

/**
 * The compiled HTML the preview carries. The layout goes along once it's
 * been opened (saved or not, so an edit shows on every email); an email's
 * own HTML only while that email is in MJML mode on screen. Each is the
 * last good compile, so a typo keeps the last preview.
 */
export function previewHTML(
	selected: Part,
	mode: Mode,
	compiled: Record<Part, { html: string } | undefined>
): { layout_html?: string; html?: string } {
	const out: { layout_html?: string; html?: string } = {};
	const layout = compiled['layout']?.html;
	if (layout) out.layout_html = layout;
	const own = compiled[selected]?.html;
	if (selected !== 'layout' && mode === 'mjml' && own) out.html = own;
	return out;
}
