<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { basicSetup } from 'codemirror';
	import { EditorState, Compartment } from '@codemirror/state';
	import { Decoration, EditorView, MatchDecorator, ViewPlugin, type ViewUpdate } from '@codemirror/view';
	import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
	import { lintGutter, setDiagnostics, type Diagnostic as CmDiagnostic } from '@codemirror/lint';
	import { xml } from '@codemirror/lang-xml';
	import { tags } from '@lezer/highlight';
	import { MJML_ELEMENTS } from '$lib/mjml/schema';
	import type { Diagnostic } from '$lib/mjml/compile';

	let {
		value,
		diagnostics = [],
		readonly = false,
		label,
		onchange
	}: {
		value: string;
		diagnostics?: Diagnostic[];
		readonly?: boolean;
		label: string;
		onchange: (v: string) => void;
	} = $props();

	let host: HTMLDivElement;
	let view: EditorView | undefined;
	const ro = new Compartment();

	// Colours come from the panel's tokens, so the editor follows its theme.
	const highlight = HighlightStyle.define([
		{ tag: [tags.tagName, tags.angleBracket], color: 'var(--cm-tag)' },
		{ tag: tags.attributeName, color: 'var(--cm-attr)' },
		{ tag: tags.attributeValue, color: 'var(--cm-value)' },
		{
			tag: [tags.comment, tags.meta, tags.processingInstruction],
			color: 'var(--fg-quiet)',
			fontStyle: 'italic'
		},
		{ tag: tags.invalid, color: 'var(--danger-text)' }
	]);

	// {{placeholders}} stand out.
	const phMark = Decoration.mark({ class: 'cm-ph' });
	const phDecorator = new MatchDecorator({ regexp: /\{\{\s*[A-Za-z_]\w*\s*\}\}/g, decoration: () => phMark });
	const placeholders = ViewPlugin.define(
		(v) => ({
			decorations: phDecorator.createDeco(v),
			update(u: ViewUpdate) {
				this.decorations = phDecorator.updateDeco(u, this.decorations);
			}
		}),
		{ decorations: (p) => p.decorations }
	);

	const theme = EditorView.theme({
		'&': {
			fontSize: 'var(--text-sm)',
			backgroundColor: 'var(--surface)',
			color: 'var(--fg)',
			height: '100%'
		},
		'.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.55' },
		'.cm-content': { caretColor: 'var(--accent)' },
		'.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--accent)' },
		'&.cm-focused': { outline: 'none' },
		'.cm-gutters': {
			backgroundColor: 'var(--surface-raised)',
			color: 'var(--fg-quiet)',
			borderRight: '1px solid var(--border-soft)'
		},
		'.cm-activeLine': { backgroundColor: 'color-mix(in oklab, var(--surface-hover), transparent 40%)' },
		'.cm-activeLineGutter': { backgroundColor: 'var(--surface-hover)', color: 'var(--fg)' },
		'&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': {
			backgroundColor: 'var(--accent-dim) !important'
		},
		'.cm-matchingBracket': { backgroundColor: 'var(--accent-dim)', outline: '1px solid var(--accent)' },
		'.cm-ph': {
			color: 'var(--accent-text)',
			backgroundColor: 'var(--accent-dim)',
			borderRadius: '2px'
		},
		'.cm-tooltip': {
			backgroundColor: 'var(--surface-raised)',
			color: 'var(--fg)',
			border: '1px solid var(--border-strong)'
		},
		'.cm-tooltip-autocomplete > ul > li[aria-selected]': {
			backgroundColor: 'var(--accent-dim)',
			color: 'var(--fg)'
		},
		'.cm-panels': { backgroundColor: 'var(--surface-raised)', color: 'var(--fg)' },
		'.cm-diagnostic-error': { borderLeftColor: 'var(--danger)' },
		'.cm-diagnostic-warning': { borderLeftColor: 'var(--warn)' },
		'.cm-foldPlaceholder': { backgroundColor: 'var(--surface-hover)', border: 'none', color: 'var(--fg-2)' }
	});

	function cmDiagnostics(state: EditorState, list: Diagnostic[]): CmDiagnostic[] {
		const lines = state.doc.lines;
		return list.map((d) => {
			const l = state.doc.line(Math.min(Math.max(1, d.line), lines));
			// The line's text, not its leading indentation.
			const indent = /^\s*/.exec(l.text)?.[0].length ?? 0;
			return {
				from: Math.min(l.from + indent, l.to),
				to: l.to,
				severity: d.severity,
				message: `Line ${d.line}: ${d.message}`
			};
		});
	}

	onMount(() => {
		view = new EditorView({
			parent: host,
			state: EditorState.create({
				doc: value,
				extensions: [
					basicSetup,
					xml({ elements: MJML_ELEMENTS }),
					syntaxHighlighting(highlight),
					placeholders,
					lintGutter(),
					theme,
					EditorView.lineWrapping,
					EditorView.contentAttributes.of({ 'aria-label': label }),
					ro.of(EditorState.readOnly.of(readonly)),
					EditorView.updateListener.of((u) => {
						if (u.docChanged) onchange(u.state.doc.toString());
					})
				]
			})
		});
		view.dispatch(setDiagnostics(view.state, cmDiagnostics(view.state, diagnostics)));
	});
	onDestroy(() => view?.destroy());

	// A new value from outside (reset, a reload) replaces the text.
	$effect(() => {
		const v = value;
		if (view && v !== view.state.doc.toString())
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
	});
	$effect(() => {
		const d = diagnostics;
		if (view) view.dispatch(setDiagnostics(view.state, cmDiagnostics(view.state, d)));
	});
	$effect(() => {
		const r = readonly;
		view?.dispatch({ effects: ro.reconfigure(EditorState.readOnly.of(r)) });
	});

	/** Inserts text at the cursor (replacing the selection). */
	export function insert(text: string) {
		if (!view || readonly) return;
		const sel = view.state.selection.main;
		view.dispatch({
			changes: { from: sel.from, to: sel.to, insert: text },
			selection: { anchor: sel.from + text.length },
			scrollIntoView: true
		});
		view.focus();
	}

	/** Puts the cursor on a line (from the problem list). */
	export function goToLine(n: number) {
		if (!view) return;
		const l = view.state.doc.line(Math.min(Math.max(1, n), view.state.doc.lines));
		view.dispatch({ selection: { anchor: l.from }, scrollIntoView: true });
		view.focus();
	}
</script>

<div class="cm-host" bind:this={host} data-testid="mjml-editor"></div>

<style>
	.cm-host {
		--cm-tag: var(--info-text);
		--cm-attr: var(--accent-text);
		--cm-value: var(--success-text);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		overflow: hidden;
		height: 440px;
		min-height: 220px;
		resize: vertical;
		background: var(--surface);
	}
	.cm-host:focus-within {
		border-color: var(--accent);
		box-shadow:
			0 0 0 1px var(--accent),
			0 0 0 4px var(--accent-dim);
	}
	@media (max-width: 700px) {
		.cm-host {
			height: 360px;
		}
	}
</style>
