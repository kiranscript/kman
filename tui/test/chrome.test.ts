import { describe, expect, it } from 'vitest';
import { fitHints } from '../src/ui/chrome.js';

const hints = [
  { k: '↑↓', label: 'select', always: true },
  { k: 'n', label: 'new' },
  { k: 'd', label: 'disable' },
  { k: 'x', label: 'revoke' },
  { k: 'r', label: 'refresh' },
  { k: 'q', label: 'quit', always: true },
];

describe('fitHints', () => {
  it('keeps everything that fits', () => {
    expect(fitHints(hints, 200)).toHaveLength(6);
  });
  it('drops from the end, and keeps the ones marked always', () => {
    const out = fitHints(hints, 36).map((h) => h.k);
    expect(out).toEqual(['↑↓', 'n', 'd', 'q']);
    expect(fitHints(hints, 1).map((h) => h.k)).toEqual(['↑↓', 'q']);
  });
});
