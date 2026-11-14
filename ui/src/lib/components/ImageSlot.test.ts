import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';
import ImageSlot from './ImageSlot.svelte';

const upload = vi.fn();
vi.mock('$lib/api', () => ({ upload: (...a: unknown[]) => upload(...a), api: vi.fn() }));
vi.mock('$lib/toasts.svelte', () => ({ toasts: { push: vi.fn() }, errorText: (e: Error) => e.message }));

/** jsdom has no DataTransfer: the parts the slot reads. */
const files = (...f: File[]) => ({ dataTransfer: { types: ['Files'], files: f, dropEffect: 'none' } });
const png = (name = 'logo.png', size = 10) => new File([new Uint8Array(size)], name, { type: 'image/png' });

function slot(props: Record<string, unknown> = {}) {
	const onsaved = vi.fn();
	const r = render(ImageSlot, { slot: 'logo', label: 'Logo', onsaved, ...props });
	return { onsaved, el: r.container.querySelector('.slot') as HTMLElement };
}

beforeEach(() => upload.mockReset().mockResolvedValue({ config: { ok: true } }));

describe('ImageSlot drag and drop', () => {
	it('uploads a dropped image the same way as a chosen one', async () => {
		const { el, onsaved } = slot();
		const f = png();
		await fireEvent.drop(el, files(f, png('second.png')));
		expect(upload).toHaveBeenCalledOnce();
		expect(upload).toHaveBeenCalledWith('PUT', '/branding/images/logo', f);
		await vi.waitFor(() => expect(onsaved).toHaveBeenCalledWith({ ok: true }));
	});

	it('uploads a chosen file', async () => {
		const { onsaved } = slot();
		const input = screen.getByLabelText(/Upload/) as HTMLInputElement;
		const f = png();
		Object.defineProperty(input, 'files', { value: [f], configurable: true });
		await fireEvent.change(input);
		expect(upload).toHaveBeenCalledWith('PUT', '/branding/images/logo', f);
		await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
	});

	it('rejects a non-image drop inline', async () => {
		const { el } = slot();
		await fireEvent.drop(el, files(new File(['hi'], 'notes.txt', { type: 'text/plain' })));
		expect(upload).not.toHaveBeenCalled();
		expect(screen.getByRole('alert').textContent).toBe('That’s not a PNG, JPEG, WebP, GIF or SVG image.');
	});

	it('rejects an image type outside accept', async () => {
		const { el } = slot();
		await fireEvent.drop(el, files(new File(['x'], 'a.bmp', { type: 'image/bmp' })));
		expect(upload).not.toHaveBeenCalled();
		expect(screen.getByRole('alert')).toBeTruthy();
	});

	it('keeps the 2 MB limit', async () => {
		const { el } = slot();
		await fireEvent.drop(el, files(png('big.png', 3 * 1024 * 1024)));
		expect(upload).not.toHaveBeenCalled();
		expect(screen.getByRole('alert').textContent).toMatch(/2 MB at most/);
	});

	it('highlights while a file is over it, without flickering over children', async () => {
		const { el } = slot();
		expect(screen.getByText('or drop an image here')).toBeTruthy();
		await fireEvent.dragEnter(el, files());
		await fireEvent.dragEnter(el.querySelector('.thumb')!, files()); // bubbles: depth 2
		await fireEvent.dragLeave(el, files()); // leaving the slot for the child: depth 1
		expect(el.classList.contains('over')).toBe(true);
		expect(screen.getByText('Drop to upload')).toBeTruthy();
		await fireEvent.dragLeave(el.querySelector('.thumb')!, files());
		expect(el.classList.contains('over')).toBe(false);
	});

	it('only claims dragover for file drags', async () => {
		const { el } = slot();
		const text = new Event('dragover', { bubbles: true, cancelable: true });
		Object.assign(text, { dataTransfer: { types: ['text/plain'], files: [] } });
		el.dispatchEvent(text);
		expect(text.defaultPrevented).toBe(false);
		const file = new Event('dragover', { bubbles: true, cancelable: true });
		Object.assign(file, files());
		el.dispatchEvent(file);
		expect(file.defaultPrevented).toBe(true);
	});

	it('ignores drops while disabled, with no highlight', async () => {
		const { el } = slot({ disabled: true });
		expect(screen.queryByText('or drop an image here')).toBeNull();
		await fireEvent.dragEnter(el, files());
		expect(el.classList.contains('over')).toBe(false);
		await fireEvent.drop(el, files(png()));
		expect(upload).not.toHaveBeenCalled();
	});

	it('ignores drops while an upload is in flight', async () => {
		let finish!: (v: unknown) => void;
		upload.mockReturnValueOnce(new Promise((r) => (finish = r)));
		const { el } = slot();
		await fireEvent.drop(el, files(png()));
		await fireEvent.dragEnter(el, files());
		expect(el.classList.contains('over')).toBe(false);
		await fireEvent.drop(el, files(png()));
		expect(upload).toHaveBeenCalledOnce();
		finish({ config: {} });
	});
});
