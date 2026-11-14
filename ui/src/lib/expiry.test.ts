import { describe, expect, it } from 'vitest';
import { customDaysError, expiryChoice, expiryDays, MAX_EXPIRY_DAYS } from './expiry';

describe('channel expiry', () => {
	it('maps stored days to a preset or Custom', () => {
		expect(expiryChoice(0)).toBe('0');
		expect(expiryChoice(90)).toBe('90');
		expect(expiryChoice(365)).toBe('365');
		expect(expiryChoice(45)).toBe('custom');
		expect(expiryChoice(MAX_EXPIRY_DAYS)).toBe('custom');
	});

	it('validates custom days', () => {
		expect(customDaysError(45)).toBe('');
		expect(customDaysError(1)).toBe('');
		expect(customDaysError(MAX_EXPIRY_DAYS)).toBe('');
		expect(customDaysError(0)).toMatch(/1 to 3,650 days/);
		expect(customDaysError(MAX_EXPIRY_DAYS + 1)).toMatch(/1 to 3,650 days/);
		expect(customDaysError(-5)).toMatch(/1 to 3,650/);
		expect(customDaysError(2.5)).toBe('Use whole days.');
		expect(customDaysError(null)).toBe('Enter a number of days.');
		expect(customDaysError(undefined)).toBe('Enter a number of days.');
	});

	it('turns a choice into days to save', () => {
		expect(expiryDays('0', 45)).toBe(0);
		expect(expiryDays('180', null)).toBe(180);
		expect(expiryDays('custom', 45)).toBe(45);
		expect(expiryDays('custom', 0)).toBeNull();
		expect(expiryDays('custom', null)).toBeNull();
	});
});
