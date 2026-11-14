import { describe, expect, it } from 'vitest';
import { hexToHsv, hsvToHex, normalizeHex, presets } from './color';

describe('color', () => {
	it('round-trips hex through HSV', () => {
		for (const { hex } of [...presets, { hex: '#000000' }, { hex: '#ffffff' }, { hex: '#808080' }]) {
			const h = hexToHsv(hex)!;
			expect(hsvToHex(h.h, h.s, h.v)).toBe(hex);
		}
	});
	it('reads the usual corners', () => {
		expect(hsvToHex(0, 1, 1)).toBe('#ff0000');
		expect(hsvToHex(120, 1, 1)).toBe('#00ff00');
		expect(hsvToHex(240, 1, 0.5)).toBe('#000080');
		expect(hexToHsv('nope')).toBeNull();
	});
	it('normalises typed hex', () => {
		expect(normalizeHex('D97706')).toBe('#d97706');
		expect(normalizeHex('#abc')).toBe('#aabbcc');
		expect(normalizeHex('#abcd')).toBeNull();
	});
});
