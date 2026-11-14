import type { DropdownOption } from './dropdown';

/** The longest channel expiry the auth service accepts, in days. */
export const MAX_EXPIRY_DAYS = 3650;

export type ExpiryChoice = '0' | '30' | '90' | '180' | '365' | 'custom';

export const expiryOptions: DropdownOption<ExpiryChoice>[] = [
	{ value: '0', label: 'Never', description: 'Registered channels stay registered' },
	{ value: '30', label: '30 days' },
	{ value: '90', label: '90 days' },
	{ value: '180', label: '180 days' },
	{ value: '365', label: '1 year' },
	{ value: 'custom', label: 'Custom…', description: `1 to ${MAX_EXPIRY_DAYS.toLocaleString('en')} days` }
];

const presets = new Set<string>(expiryOptions.map((o) => o.value).filter((v) => v !== 'custom'));

/** The dropdown choice for a stored value: a preset, or Custom (45 → Custom). */
export function expiryChoice(days: number): ExpiryChoice {
	const v = String(days);
	return presets.has(v) ? (v as ExpiryChoice) : 'custom';
}

/** Why a custom number of days can't be saved, or '' when it can. */
export function customDaysError(days: number | null | undefined): string {
	if (days === null || days === undefined || Number.isNaN(days)) return 'Enter a number of days.';
	if (!Number.isInteger(days)) return 'Use whole days.';
	if (days < 1 || days > MAX_EXPIRY_DAYS)
		return `Enter 1 to ${MAX_EXPIRY_DAYS.toLocaleString('en')} days, or pick Never.`;
	return '';
}

/** The days to store for a choice, or null while a custom value is invalid. */
export function expiryDays(choice: ExpiryChoice, custom: number | null | undefined): number | null {
	if (choice !== 'custom') return Number(choice);
	return customDaysError(custom) ? null : (custom as number);
}
