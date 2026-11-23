// Every dropdown in the panel is <Dropdown> ($lib/components/Dropdown.svelte):
// it looks the same in every browser and theme, and `free` covers "type
// anything, with suggestions". This keeps native <select> and <datalist>
// (and their <option>s) from coming back.
import { describe, expect, it } from 'vitest';

const sources = import.meta.glob(
	['/src/**/*.{svelte,html,ts,js}', '/static/**/*.html', '!/src/no-native-select.test.ts'],
	{
		query: '?raw',
		import: 'default',
		eager: true
	}
) as Record<string, string>;

const banned = /<\s*(select|datalist|option)\b/i;

describe('no native dropdowns', () => {
	it('scans the UI sources', () => {
		expect(Object.keys(sources)).toContain('/src/lib/components/Dropdown.svelte');
		expect(Object.keys(sources)).toContain('/src/routes/directory/+page.svelte');
	});

	it('uses <Dropdown> instead of <select>, <datalist> or <option>', () => {
		const hits: string[] = [];
		for (const [file, text] of Object.entries(sources)) {
			text.split('\n').forEach((line, i) => {
				if (banned.test(line)) hits.push(`${file}:${i + 1}: ${line.trim()}`);
			});
		}
		expect(hits, 'use $lib/components/Dropdown.svelte (`free` for free text with suggestions)').toEqual([]);
	});
});
