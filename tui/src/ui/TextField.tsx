import { Text } from 'ink';
import type { Key } from 'ink';
import { graphemes, graphemeWidth, padEnd, tailCells } from '../format.js';
import { color } from './theme.js';

/**
 * The text after one key press: characters append, backspace deletes, ctrl+u
 * clears. Anything else (arrows, enter, escape, shortcuts) leaves it alone, so
 * the caller can use those keys for navigation.
 */
export function editText(value: string, input: string, key: Key, max = 64): string {
  if (key.backspace || key.delete) return graphemes(value).slice(0, -1).join('');
  if (key.ctrl) return input === 'u' ? '' : value;
  if (key.meta || key.escape || key.return || key.tab || key.upArrow || key.downArrow || key.leftArrow || key.rightArrow) {
    return value;
  }
  // Pasted text arrives as one chunk. Control characters are dropped, never typed in.
  // eslint-disable-next-line no-control-regex
  const clean = input.replace(/[\u0000-\u001f\u007f]/g, '');
  return clean ? graphemes(value + clean).slice(0, max).join('') : value;
}

export interface Edit {
  value: string;
  /** Where the cursor is, 0 to the value's length: the characters before it are to its left. */
  cursor: number;
}

/**
 * One key press on a field with a cursor. ← → Home End (and ctrl+a, ctrl+e) move it,
 * backspace deletes to its left, delete to its right, ctrl+u clears the field, and
 * typed or pasted text goes in at the cursor. `accept` cleans what is typed in. Up,
 * down, tab, enter and escape are left to the caller.
 */
export function editField(state: Edit, input: string, key: Key, max = 64, accept: (text: string) => string = (t) => t): Edit {
  // Whole characters as a person sees them: a joined emoji is one, so the cursor, delete
  // and typing never land inside it.
  const chars = graphemes(state.value);
  const at = Math.min(state.cursor, chars.length);
  const make = (c: string[], cursor: number): Edit => ({ value: c.join(''), cursor });
  if (key.leftArrow) return make(chars, Math.max(0, at - 1));
  if (key.rightArrow) return make(chars, Math.min(chars.length, at + 1));
  if (key.home || (key.ctrl && input === 'a')) return make(chars, 0);
  if (key.end || (key.ctrl && input === 'e')) return make(chars, chars.length);
  // Terminals send DEL for the backspace key, which Ink reports as `delete`; a real forward
  // delete arrives as an escape sequence and is not told apart, so both delete to the left.
  if (key.backspace || key.delete) return at === 0 ? make(chars, 0) : make([...chars.slice(0, at - 1), ...chars.slice(at)], at - 1);
  if (key.ctrl) return input === 'u' ? make([], 0) : make(chars, at);
  if (key.meta || key.escape || key.return || key.tab || key.upArrow || key.downArrow) return make(chars, at);
  // Pasted text arrives as one chunk. Control characters are dropped, never typed in.
  // eslint-disable-next-line no-control-regex
  const typed = graphemes(accept(input.replace(/[\u0000-\u001f\u007f]/g, '')));
  if (typed.length === 0) return make(chars, at);
  const room = Math.max(0, max - chars.length);
  const added = typed.slice(0, room);
  // The cursor is counted in whole characters of the new text: what is typed may join the
  // character before it (an accent), and then it is part of that one, not a new one.
  const before = [...chars.slice(0, at), ...added].join('');
  return { value: before + chars.slice(at).join(''), cursor: graphemes(before).length };
}

/** What an unfocused field shows: the end of a long value, behind a mark that says its start is cut. */
function unfocusedText(value: string, width: number): string {
  return tailCells(value, width);
}

interface Props {
  value: string;
  focused: boolean;
  width: number;
  placeholder?: string;
  /** The cursor, when it is not at the end. */
  cursor?: number;
}

/** A single-line field: brackets, the text, and a block cursor while focused. Always `width + 2` wide. */
export function TextField({ value, focused, width, placeholder, cursor }: Props) {
  const empty = value === '';
  const edge = (c: string) => (
    <Text color={focused ? color.accent : undefined} dimColor={!focused}>
      {c}
    </Text>
  );
  if (!focused) {
    return (
      <Text>
        {edge('[')}
        <Text dimColor={empty}>{padEnd(empty ? (placeholder ?? '') : unfocusedText(value, width), width)}</Text>
        {edge(']')}
      </Text>
    );
  }
  // Focused: when empty the cursor sits on the placeholder's first character, so nothing
  // shifts and the placeholder never reads as typed text.
  if (empty) {
    const ph = [...padEnd(placeholder ?? '', width)];
    return (
      <Text>
        {edge('[')}
        <Text inverse>{ph[0]}</Text>
        <Text dimColor>{ph.slice(1).join('')}</Text>
        {edge(']')}
      </Text>
    );
  }
  // The text and one more cell for a cursor at its end; a window of `width` terminal cells
  // (a wide character is two), kept around the cursor, which sits at the end of the text
  // unless it was moved.
  const cells = [...graphemes(value), ' '];
  const w = cells.map(graphemeWidth);
  const at = Math.min(cursor ?? cells.length - 1, cells.length - 1);
  let from = at;
  let to = at + 1;
  let used = w[at]!;
  for (let grew = true; grew; ) {
    grew = false;
    if (from > 0 && used + w[from - 1]! <= width) {
      from--;
      used += w[from]!;
      grew = true;
    }
    if (to < cells.length && used + w[to]! <= width) {
      used += w[to]!;
      to++;
      grew = true;
    }
  }
  const view = cells.slice(from, to);
  const i = at - from;
  // A mark where text is cut off, so a scrolled field does not look as if that were all of it.
  if (from > 0 && i !== 0) {
    used -= w[from]! - 1;
    view[0] = '…';
  }
  if (to < cells.length && i !== view.length - 1) {
    used -= w[to - 1]! - 1;
    view[view.length - 1] = '…';
  }
  return (
    <Text>
      {edge('[')}
      <Text>{view.slice(0, i).join('')}</Text>
      <Text inverse>{view[i] ?? ' '}</Text>
      <Text>{view.slice(i + 1).join('')}</Text>
      <Text>{' '.repeat(Math.max(0, width - used))}</Text>
      {edge(']')}
    </Text>
  );
}
