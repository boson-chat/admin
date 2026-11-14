import type { ElementSpec } from '@codemirror/lang-xml';

/**
 * MJML's elements for the editor's tag and attribute completion (mjml.io
 * docs, 4.x/5.x). Not a validator: the compiler reports what's wrong.
 */
const common = ['css-class', 'mj-class'];
const box = ['padding', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left'];
const font = [
	'color',
	'font-family',
	'font-size',
	'font-style',
	'font-weight',
	'line-height',
	'letter-spacing'
];
const blocks = [
	'mj-text',
	'mj-button',
	'mj-image',
	'mj-divider',
	'mj-spacer',
	'mj-social',
	'mj-table',
	'mj-navbar',
	'mj-accordion',
	'mj-carousel',
	'mj-raw'
];

const el = (name: string, children: string[], attributes: string[], top = false): ElementSpec => ({
	name,
	children,
	attributes: [...attributes, ...common],
	top
});

export const MJML_ELEMENTS: ElementSpec[] = [
	el('mjml', ['mj-head', 'mj-body'], ['lang', 'dir', 'owa'], true),
	el(
		'mj-head',
		[
			'mj-attributes',
			'mj-breakpoint',
			'mj-font',
			'mj-html-attributes',
			'mj-preview',
			'mj-style',
			'mj-title',
			'mj-raw'
		],
		[]
	),
	el(
		'mj-attributes',
		['mj-all', 'mj-class', ...blocks, 'mj-section', 'mj-column', 'mj-wrapper', 'mj-body'],
		[]
	),
	el('mj-all', [], [...font, ...box]),
	el('mj-class', [], ['name', ...font, ...box]),
	el('mj-breakpoint', [], ['width']),
	el('mj-font', [], ['name', 'href']),
	el('mj-html-attributes', ['mj-selector'], []),
	el('mj-selector', ['mj-html-attribute'], ['path']),
	el('mj-html-attribute', [], ['name']),
	el('mj-preview', [], []),
	el('mj-style', [], ['inline']),
	el('mj-title', [], []),
	el('mj-body', ['mj-section', 'mj-wrapper', 'mj-hero', 'mj-raw'], ['background-color', 'width']),
	el(
		'mj-wrapper',
		['mj-section', 'mj-hero', 'mj-raw'],
		['background-color', 'background-url', 'border', 'border-radius', 'full-width', 'text-align', ...box]
	),
	el(
		'mj-section',
		['mj-column', 'mj-group', 'mj-raw'],
		[
			'background-color',
			'background-url',
			'background-size',
			'background-repeat',
			'border',
			'border-radius',
			'direction',
			'full-width',
			'text-align',
			...box
		]
	),
	el('mj-group', ['mj-column', 'mj-raw'], ['width', 'background-color', 'direction', 'vertical-align']),
	el('mj-column', blocks, [
		'width',
		'background-color',
		'inner-background-color',
		'border',
		'border-radius',
		'inner-border-radius',
		'vertical-align',
		...box
	]),
	el('mj-hero', blocks, [
		'mode',
		'height',
		'background-url',
		'background-width',
		'background-height',
		'background-color',
		...box
	]),
	el(
		'mj-text',
		[],
		[...font, 'align', 'container-background-color', 'height', 'text-decoration', 'text-transform', ...box]
	),
	el(
		'mj-button',
		[],
		[
			'href',
			'target',
			'rel',
			'background-color',
			'border',
			'border-radius',
			'inner-padding',
			'align',
			'width',
			'height',
			'text-align',
			'text-decoration',
			'text-transform',
			...font,
			...box
		]
	),
	el(
		'mj-image',
		[],
		['src', 'alt', 'href', 'width', 'height', 'align', 'border', 'border-radius', 'title', 'target', ...box]
	),
	el('mj-divider', [], ['border-color', 'border-style', 'border-width', 'width', 'align', ...box]),
	el('mj-spacer', [], ['height', 'container-background-color']),
	el('mj-social', ['mj-social-element'], ['mode', 'align', 'icon-size', 'icon-padding', ...font, ...box]),
	el(
		'mj-social-element',
		[],
		['name', 'href', 'src', 'alt', 'background-color', 'icon-size', ...font, ...box]
	),
	el(
		'mj-table',
		[],
		['width', 'align', 'border', 'cellpadding', 'cellspacing', 'table-layout', ...font, ...box]
	),
	el('mj-navbar', ['mj-navbar-link'], ['align', 'hamburger', 'base-url']),
	el('mj-navbar-link', [], ['href', 'target', ...font, ...box]),
	el('mj-accordion', ['mj-accordion-element'], ['border', 'font-family', 'icon-position']),
	el('mj-accordion-element', ['mj-accordion-title', 'mj-accordion-text'], ['background-color', 'border']),
	el('mj-accordion-title', [], [...font, 'background-color', ...box]),
	el('mj-accordion-text', [], [...font, 'background-color', ...box]),
	el('mj-carousel', ['mj-carousel-image'], ['align', 'border-radius', 'icon-width', 'thumbnails']),
	el('mj-carousel-image', [], ['src', 'alt', 'href', 'title']),
	el('mj-raw', [], [])
];
