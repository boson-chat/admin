import { describe, expect, it, vi } from 'vitest';
import { checkPlaceholders, checkStructure, compile, lineAt, placeholdersFor, type MjmlFn } from './compile';

const layout = `<mjml>
  <mj-body>
    <mj-section>
      <mj-column>
        <mj-text>Hello<br>there {{ content }}</mj-text>
        <mj-raw><img src="x"><p>raw</mj-raw>
      </mj-column>
    </mj-section>
  </mj-body>
</mjml>`;

/** A stand-in for mjml-browser: echoes the source, reports what it's told. */
function fakeMjml(errors: { line: number; message: string }[] = []) {
	return vi.fn<MjmlFn>(async (src) => ({ html: `<html>${src}</html>`, errors }));
}

describe('checkStructure', () => {
	it('accepts MJML whose ending tags hold HTML', () => {
		expect(checkStructure(layout)).toEqual([]);
		expect(checkStructure('<mjml><!-- <mj-x> --><mj-body/></mjml>')).toEqual([]);
		expect(
			checkStructure('<mjml><mj-head><mj-style>a > b { color: red }</mj-style></mj-head></mjml>')
		).toEqual([]);
		expect(checkStructure(`<mjml><mj-button href="a>b">x</mj-button></mjml>`)).toEqual([]);
	});

	it('reports unclosed and mismatched tags on their lines', () => {
		const src = '<mjml>\n<mj-body>\n<mj-section>\n<mj-column>\n</mj-section>\n</mj-body>\n</mjml>';
		expect(checkStructure(src)).toEqual([
			{
				line: 4,
				severity: 'error',
				message: '<mj-column> is never closed (expected </mj-column> before </mj-section>).'
			}
		]);
		expect(checkStructure('<mjml>\n<mj-body>\n')).toEqual([
			{ line: 2, severity: 'error', message: '<mj-body> is never closed.' },
			{ line: 1, severity: 'error', message: '<mjml> is never closed.' }
		]);
		expect(checkStructure('<mjml>\n</mj-body></mjml>')[0]).toMatchObject({ line: 2 });
		expect(checkStructure('<mjml>\n<mj-text>open')[0].message).toBe('<mj-text> is never closed.');
		expect(checkStructure('<mjml>\n<mj-body\n')[0]).toMatchObject({ line: 2 });
		expect(checkStructure('<mjml><!-- x')[0].message).toMatch(/comment/);
	});

	it('wants one <mjml> root', () => {
		expect(checkStructure('<div></div>')[0].message).toBe('The root element must be <mjml>, not <div>.');
		expect(checkStructure('just text')[0].message).toMatch(/needs an <mjml> root/);
		expect(checkStructure('<mjml></mjml>\n<mj-body/>')[0]).toMatchObject({ line: 2 });
	});
});

describe('checkPlaceholders', () => {
	it('needs {{content}} exactly once in the layout', () => {
		expect(checkPlaceholders('<mjml>{{content}}</mjml>', 'layout')).toEqual([]);
		expect(checkPlaceholders('<mjml></mjml>', 'layout')[0]).toMatchObject({ severity: 'error', line: 1 });
		expect(checkPlaceholders('{{content}}\n{{ content }}', 'layout')).toEqual([
			{ line: 2, severity: 'error', message: '{{content}} may appear only once in the layout.' }
		]);
	});

	it('needs {{link}} in an email of its own; warns about the rest', () => {
		expect(checkPlaceholders('<a href="{{link}}">', 'invite')).toEqual([]);
		expect(checkPlaceholders('<p>no link</p>', 'invite')[0].severity).toBe('error');
		const d = checkPlaceholders('{{link}}\n{{content}}\n{{nope}}', 'invite');
		expect(d.map((x) => [x.line, x.severity])).toEqual([
			[2, 'warning'],
			[3, 'warning']
		]);
		expect(d[1].message).toContain('{{nope}}');
	});

	it('lists the placeholders per part', () => {
		expect(placeholdersFor('layout')).toContain('content');
		expect(placeholdersFor('invite')).not.toContain('content');
		expect(placeholdersFor('invite')).toContain('logo_url');
	});
});

describe('compile', () => {
	it('compiles and maps MJML’s errors to lines', async () => {
		const mjml = fakeMjml([{ line: 5, message: 'Attribute color has invalid value: nope for type Color ' }]);
		const r = await compile(mjml, layout, 'layout');
		expect(mjml).toHaveBeenCalledWith(layout, expect.objectContaining({ validationLevel: 'soft' }));
		expect(r.html).toContain('{{ content }}');
		expect(r.ok).toBe(false);
		expect(r.diagnostics).toEqual([
			{ line: 5, severity: 'error', message: 'Attribute color has invalid value: nope for type Color' }
		]);
	});

	it('doesn’t count a placeholder as an invalid attribute value', async () => {
		const mjml = fakeMjml([
			{ line: 2, message: 'Attribute color has invalid value: {{accent}} for type Color ' }
		]);
		const r = await compile(mjml, layout, 'layout');
		expect(r).toMatchObject({ ok: true, diagnostics: [] });
	});

	it('is ok without problems; warnings don’t block', async () => {
		const r = await compile(fakeMjml(), layout.replace('there', 'there {{mystery}}'), 'layout');
		expect(r.ok).toBe(true);
		expect(r.diagnostics).toHaveLength(1);
		expect(r.diagnostics[0]).toMatchObject({ line: 5, severity: 'warning' });
	});

	it('doesn’t run MJML on a broken structure', async () => {
		const mjml = fakeMjml();
		const r = await compile(mjml, '<mjml>\n<mj-body>', 'layout');
		expect(mjml).not.toHaveBeenCalled();
		expect(r).toMatchObject({ html: '', ok: false });
		expect(r.diagnostics.map((d) => d.line)).toEqual([1, 1, 2]);
	});

	it('reports a compiler that throws, and an empty source', async () => {
		const boom: MjmlFn = () => {
			throw new Error('Malformed MJML on line 3');
		};
		const r = await compile(boom, layout, 'layout');
		expect(r).toMatchObject({ html: '', ok: false });
		expect(r.diagnostics[0]).toMatchObject({ line: 3, message: 'Malformed MJML on line 3' });
		expect((await compile(fakeMjml(), '  ', 'layout')).diagnostics[0].message).toBe('The MJML is empty.');
	});

	it('takes a synchronous mjml (4.x) too', async () => {
		const sync: MjmlFn = (src) => ({ html: src.toUpperCase(), errors: [] });
		expect((await compile(sync, '<mjml>{{link}}</mjml>', 'invite')).html).toBe('<MJML>{{LINK}}</MJML>');
	});

	it('counts lines', () => {
		expect(lineAt('a\nb\nc', 0)).toBe(1);
		expect(lineAt('a\nb\nc', 4)).toBe(3);
	});
});
