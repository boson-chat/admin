import { describe, expect, it } from 'vitest';
import {
	afterRemove,
	afterSave,
	discard,
	initialMode,
	isDirty,
	loaded,
	previewHTML,
	resetTo
} from './drafts';

describe('MJML drafts', () => {
	it('starts from the saved source, else the default, clean', () => {
		const a = loaded('<mjml>saved</mjml>', '<mjml>default</mjml>');
		expect(a).toEqual({
			saved: '<mjml>saved</mjml>',
			base: '<mjml>saved</mjml>',
			draft: '<mjml>saved</mjml>'
		});
		const b = loaded(null, '<mjml>default</mjml>');
		expect(b.base).toBe('<mjml>default</mjml>');
		expect(isDirty(a) || isDirty(b) || isDirty(undefined)).toBe(false);
	});

	it('is dirty while the text differs from where it started', () => {
		const d = { ...loaded(null, 'D'), draft: 'D2' };
		expect(isDirty(d)).toBe(true);
		expect(isDirty({ ...d, draft: 'D' })).toBe(false);
		expect(isDirty(discard(d))).toBe(false);
		expect(discard(d).draft).toBe('D');
	});

	it('reset puts the default in, unsaved; save and remove settle it', () => {
		const saved = loaded('S', 'D');
		const reset = resetTo(saved, 'D');
		expect(reset).toEqual({ saved: 'S', base: 'S', draft: 'D' });
		expect(isDirty(reset)).toBe(true);

		const s = afterSave(reset, 'D');
		expect(s).toEqual({ saved: 'D', base: 'D', draft: 'D' });
		expect(isDirty(s)).toBe(false);
		// Typing went on while the save ran: that stays unsaved.
		expect(isDirty(afterSave({ ...reset, draft: 'D+' }, 'D'))).toBe(true);

		expect(afterRemove('D')).toEqual({ saved: null, base: 'D', draft: 'D' });
	});

	it('opens an email with its own MJML in MJML mode', () => {
		expect(initialMode(true)).toBe('mjml');
		expect(initialMode(false)).toBe('text');
	});

	it('sends the layout once compiled, and an email’s own HTML only in MJML mode', () => {
		const compiled = { layout: { html: 'L' }, invite: { html: 'I' }, verification: { html: '' } };
		expect(previewHTML('layout', 'mjml', compiled)).toEqual({ layout_html: 'L' });
		expect(previewHTML('invite', 'mjml', compiled)).toEqual({ layout_html: 'L', html: 'I' });
		expect(previewHTML('invite', 'text', compiled)).toEqual({ layout_html: 'L' });
		expect(previewHTML('verification', 'mjml', compiled)).toEqual({ layout_html: 'L' });
		expect(previewHTML('invite', 'mjml', {})).toEqual({});
	});
});
