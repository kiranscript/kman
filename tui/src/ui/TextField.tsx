import { Text } from 'ink';
import type { Key } from 'ink';
import { padEnd } from '../format.js';
import { color } from './theme.js';

/**
 * The text after one key press: characters append, backspace deletes, ctrl+u
 * clears. Anything else (arrows, enter, escape, shortcuts) leaves it alone, so
 * the caller can use those keys for navigation.
 */
export function editText(value: string, input: string, key: Key, max = 64): string {
  if (key.backspace || key.delete) return [...value].slice(0, -1).join('');
  if (key.ctrl) return input === 'u' ? '' : value;
  if (key.meta || key.escape || key.return || key.tab || key.upArrow || key.downArrow || key.leftArrow || key.rightArrow) {
    return value;
  }
  // Pasted text arrives as one chunk. Control characters are dropped, never typed in.
  // eslint-disable-next-line no-control-regex
  const clean = input.replace(/[\u0000-\u001f\u007f]/g, '');
  return clean ? [...(value + clean)].slice(0, max).join('') : value;
}

interface Props {
  value: string;
  focused: boolean;
  width: number;
  placeholder?: string;
}

/** A single-line field: brackets, the text, and a block cursor while focused. Always `width + 2` wide. */
export function TextField({ value, focused, width, placeholder }: Props) {
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
        <Text dimColor={empty}>{padEnd(empty ? (placeholder ?? '') : [...value].slice(-width).join(''), width)}</Text>
        {edge(']')}
      </Text>
    );
  }
  // Focused: when empty the cursor sits on the placeholder's first character, so nothing
  // shifts and the placeholder never reads as typed text.
  const room = width - 1;
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
  const shown = [...value].slice(-room).join('');
  return (
    <Text>
      {edge('[')}
      <Text>{shown}</Text>
      <Text inverse> </Text>
      <Text>{' '.repeat(Math.max(0, room - [...shown].length))}</Text>
      {edge(']')}
    </Text>
  );
}
