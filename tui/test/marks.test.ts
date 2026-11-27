import { readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { textWidth } from '../src/format.js';
import { okGlyph, severityGlyph, statusStyle } from '../src/ui/theme.js';

const src = fileURLToPath(new URL('../src', import.meta.url));

const files = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? files(join(dir, e.name)) : [join(dir, e.name)]));

describe('marks', () => {
  // A terminal may draw such a character two columns wide, as an emoji, over the next cell,
  // while Ink counts it as one: Windows Terminal does so with U+2716 and U+2714.
  it('has no character that can be drawn as an emoji anywhere in the source', () => {
    const all = files(src);
    expect(all.length).toBeGreaterThan(10);
    const found = all.flatMap((file) =>
      readFileSync(file, 'utf8')
        .split('\n')
        .flatMap((line, i) =>
          [...line.matchAll(/\p{Extended_Pictographic}/gu)].map(
            (m) => `${relative(src, file)}:${i + 1} U+${m[0].codePointAt(0)!.toString(16).toUpperCase()}`,
          ),
        ),
    );
    expect(found).toEqual([]);
  });

  it('are one column each, and no two say different things with the same character', () => {
    const marks = [...Object.values(severityGlyph), okGlyph, ...Object.values(statusStyle).map((s) => s.glyph)];
    for (const mark of marks) expect(textWidth(mark), mark).toBe(1);
    expect(new Set(marks).size).toBe(marks.length);
  });
});
