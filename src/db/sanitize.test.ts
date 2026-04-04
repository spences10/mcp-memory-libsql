import { describe, expect, it } from 'vitest';
import { sanitize_input } from './client.js';

describe('sanitize_input', () => {
	it('passes through normal text unchanged', () => {
		expect(sanitize_input('hello world')).toBe('hello world');
	});

	it('preserves newlines and tabs', () => {
		expect(sanitize_input('line1\nline2\ttab')).toBe(
			'line1\nline2\ttab',
		);
	});

	it('strips null bytes and control characters', () => {
		expect(sanitize_input('hello\x00world')).toBe('helloworld');
		expect(sanitize_input('test\x01\x02\x03value')).toBe(
			'testvalue',
		);
	});

	it('collapses excessive newlines to double', () => {
		expect(sanitize_input('a\n\n\n\nb')).toBe('a\n\nb');
		expect(sanitize_input('a\n\n\n\n\n\nb')).toBe('a\n\nb');
	});

	it('trims leading and trailing whitespace', () => {
		expect(sanitize_input('  hello  ')).toBe('hello');
		expect(sanitize_input('\n\nhello\n\n')).toBe('hello');
	});

	it('handles empty string', () => {
		expect(sanitize_input('')).toBe('');
	});

	it('handles string of only control characters', () => {
		expect(sanitize_input('\x00\x01\x02')).toBe('');
	});

	it('preserves unicode content', () => {
		expect(sanitize_input('hello 世界 🌍')).toBe('hello 世界 🌍');
	});

	it('strips bell and backspace chars', () => {
		expect(sanitize_input('test\x07\x08value')).toBe('testvalue');
	});
});
