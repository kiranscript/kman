import { render } from 'ink-testing-library';
import { describe, expect, it } from 'vitest';
import { Frame } from '../src/ui/frame.js';
import { Row } from '../src/ui/row.js';

describe('Frame', () => {
  it('sets the title and count in the top border and the position in the bottom one', () => {
    const { lastFrame } = render(
      <Frame title="[2] Keys" count="(3)" position="1 of 3" focused heavy={false} width={30} height={4}>
        <Row left={[{ t: ' one' }]} width={28} />
      </Frame>,
    );
    const lines = (lastFrame() ?? '').split('\n');
    expect(lines[0]).toBe(`╭─ [2] Keys (3) ${'─'.repeat(13)}╮`);
    expect(lines[1]).toBe(`│ one${' '.repeat(24)}│`);
    expect(lines.at(-1)).toBe(`╰${'─'.repeat(19)} 1 of 3 ─╯`);
    for (const l of lines) expect([...l]).toHaveLength(30);
  });

  it('draws the focused panel with a heavy border when there is no colour to tell it apart', () => {
    const { lastFrame } = render(
      <Frame title="Keys" focused heavy width={20} height={3}>
        <Row left={[]} width={18} />
      </Frame>,
    );
    const lines = (lastFrame() ?? '').split('\n');
    expect(lines[0]).toMatch(/^┏━ Keys ━+┓$/);
    expect(lines[1]).toMatch(/^┃.*┃$/);
    expect(lines[2]).toMatch(/^┗━+┛$/);
  });

  it('cuts a title that does not fit instead of breaking the border', () => {
    const { lastFrame } = render(
      <Frame title="a very long title indeed" heavy={false} width={14} height={3}>
        <Row left={[]} width={12} />
      </Frame>,
    );
    for (const l of (lastFrame() ?? '').split('\n')) expect([...l]).toHaveLength(14);
  });
});

describe('Row', () => {
  it('puts the right side at the right edge and cuts the left with an ellipsis when short of room', () => {
    const wide = render(<Row left={[{ t: 'name' }]} right={[{ t: '$1' }]} width={12} />);
    expect(wide.lastFrame()).toBe('name      $1');
    const narrow = render(<Row left={[{ t: 'a-long-name-here' }]} right={[{ t: '$1' }]} width={12} />);
    expect(narrow.lastFrame()).toBe('a-long-n… $1');
  });
});
