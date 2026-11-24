import { describe, expect, it } from 'vitest';
import type { Key } from 'ink';
import { editField } from '../src/ui/TextField.js';

const key = (over: Partial<Key> = {}): Key =>
  ({ upArrow: false, downArrow: false, leftArrow: false, rightArrow: false, pageDown: false, pageUp: false, home: false, end: false, return: false, escape: false, ctrl: false, shift: false, tab: false, backspace: false, delete: false, meta: false, ...over }) as Key;

describe('editField', () => {
  it('types at the cursor and moves it on', () => {
    expect(editField({ value: 'abdef', cursor: 2 }, 'c', key())).toEqual({ value: 'abcdef', cursor: 3 });
  });

  it('moves with the arrows, Home and End, and stops at the ends', () => {
    expect(editField({ value: 'abc', cursor: 0 }, '', key({ leftArrow: true })).cursor).toBe(0);
    expect(editField({ value: 'abc', cursor: 3 }, '', key({ rightArrow: true })).cursor).toBe(3);
    expect(editField({ value: 'abc', cursor: 1 }, '', key({ home: true })).cursor).toBe(0);
    expect(editField({ value: 'abc', cursor: 1 }, '', key({ end: true })).cursor).toBe(3);
  });

  it('deletes to the left of the cursor, wherever it is', () => {
    expect(editField({ value: 'abcd', cursor: 2 }, '', key({ backspace: true }))).toEqual({ value: 'acd', cursor: 1 });
    expect(editField({ value: 'abcd', cursor: 0 }, '', key({ delete: true }))).toEqual({ value: 'abcd', cursor: 0 });
  });

  it('clears on ctrl+u, respects the limit, and cleans what is typed', () => {
    expect(editField({ value: 'abc', cursor: 1 }, 'u', key({ ctrl: true }))).toEqual({ value: '', cursor: 0 });
    expect(editField({ value: 'abc', cursor: 3 }, 'defgh', key(), 5)).toEqual({ value: 'abcde', cursor: 5 });
    expect(editField({ value: '', cursor: 0 }, 'a1b2', key(), 10, (t) => t.replace(/\D/g, ''))).toEqual({ value: '12', cursor: 2 });
  });

  it('leaves navigation keys to the caller', () => {
    expect(editField({ value: 'ab', cursor: 1 }, '', key({ tab: true }))).toEqual({ value: 'ab', cursor: 1 });
    expect(editField({ value: 'ab', cursor: 1 }, '', key({ return: true }))).toEqual({ value: 'ab', cursor: 1 });
  });
});

describe('editing beside a joined emoji', () => {
  const coder = '👩\u200d💻';

  it('moves, types and deletes by whole characters, never inside the emoji', () => {
    let state = editField({ value: '', cursor: 0 }, `${coder}A`, key());
    expect(state.cursor).toBe(2);
    state = editField(state, '', key({ leftArrow: true }));
    state = editField(state, '', key({ leftArrow: true }));
    expect(state.cursor).toBe(0);
    state = editField(state, '', key({ rightArrow: true }));
    state = editField(state, 'Z', key());
    expect(state.value).toBe(`${coder}ZA`);
    expect(state.cursor).toBe(2);
    state = editField(state, '', key({ backspace: true }));
    expect(state.value).toBe(`${coder}A`);
    state = editField({ value: coder, cursor: 1 }, '', key({ backspace: true }));
    expect(state.value).toBe('');
  });
});
