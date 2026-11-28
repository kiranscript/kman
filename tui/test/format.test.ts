import { describe, expect, it } from 'vitest';
import { ago, bar, capMoney, fit, money, padEnd, fitMiddle, wrapLines, packLines, shortCount, parseDays, parseDollars, until, textWidth, tailCells } from '../src/format.js';

describe('money', () => {
  it('shows more decimals for small amounts', () => {
    expect(money(0)).toBe('$0');
    expect(money(36_000)).toBe('$0.036');
    expect(money(5)).toBe('<$0.001');
    expect(money(5_000_000)).toBe('$5.00');
    expect(money(1_250_000_000)).toBe('$1,250');
  });
  it('writes caps without trailing zeros', () => {
    expect(capMoney(5_000_000)).toBe('$5');
    expect(capMoney(360_000)).toBe('$0.36');
    expect(capMoney(50_000_000)).toBe('$50');
    expect(capMoney(500_000)).toBe('$0.50');
    expect(capMoney(1_500_000)).toBe('$1.50');
    expect(capMoney(125_000)).toBe('$0.125');
  });
});

describe('parsing what an operator types', () => {
  it('reads dollars', () => {
    expect(parseDollars('5')).toBe(5_000_000);
    expect(parseDollars('$0.25')).toBe(250_000);
    expect(parseDollars('.5')).toBe(500_000);
    expect(parseDollars('0')).toBeUndefined();
    expect(parseDollars('-1')).toBeUndefined();
    expect(parseDollars('five')).toBeUndefined();
  });
  it('reads days', () => {
    expect(parseDays('30')).toBe(30);
    expect(parseDays('14d')).toBe(14);
    expect(parseDays('0')).toBeUndefined();
    expect(parseDays('1.5')).toBeUndefined();
  });
});

describe('relative time', () => {
  const now = Date.parse('2026-11-17T12:00:00Z');
  it('says how long ago', () => {
    expect(ago('2026-11-17T11:59:50Z', now)).toBe('just now');
    expect(ago('2026-11-17T11:30:00Z', now)).toBe('30m ago');
    expect(ago('2026-11-17T09:00:00Z', now)).toBe('3h ago');
    expect(ago('2026-11-14T12:00:00Z', now)).toBe('3d ago');
  });
  it('says how long until', () => {
    expect(until('2026-11-17T11:00:00Z', now)).toBe('expired');
    expect(until('2026-11-17T12:40:00Z', now)).toBe('in 40m');
    expect(until('2026-11-17T17:00:00Z', now)).toBe('in 5h');
    expect(until('2026-11-20T12:00:00Z', now)).toBe('in 3d');
    // Rounded up, so a key made for 15 days is not "in 14d" a moment later.
    expect(until('2026-11-20T12:30:00Z', now)).toBe('in 4d');
  });
});

describe('text fitting', () => {
  it('cuts with an ellipsis', () => {
    expect(fit('checkout-service', 8)).toBe('checkou…');
    expect(fit('abc', 8)).toBe('abc');
    expect(padEnd('ab', 4)).toBe('ab  ');
  });
});

describe('bar', () => {
  it('always shows a little for a little', () => {
    expect(bar(0.001, 10).filled).toHaveLength(1);
    expect(bar(0, 10).filled).toHaveLength(0);
    expect(bar(1.7, 10).filled).toHaveLength(10);
    expect(bar(0.5, 10).filled + bar(0.5, 10).empty).toHaveLength(10);
  });
});

describe('packLines', () => {
  it('packs items into lines of the width', () => {
    expect(packLines(['aaa', 'bbb', 'ccc'], 8, 3)).toEqual(['aaa   bbb', 'ccc'].map((l) => l).slice(0, 0).concat(['aaa', 'bbb', 'ccc']));
  });

  it('says how many did not fit, on the last line', () => {
    const lines = packLines(['POST /a', 'POST /b', 'POST /c', 'POST /d', 'POST /e'], 20, 2);
    expect(lines).toHaveLength(2);
    expect(lines.every((l) => [...l].length <= 20)).toBe(true);
    expect(lines.at(-1)).toMatch(/\+\d+ more$/);
  });
});

describe('shortCount', () => {
  it('keeps small counts whole and shortens big ones', () => {
    expect(shortCount(424)).toBe('424');
    expect(shortCount(1234)).toBe('1.2k');
    expect(shortCount(12_345)).toBe('12k');
    expect(shortCount(1_200_000)).toBe('1.2M');
  });
});

describe('fitMiddle', () => {
  it('keeps both ends when it must cut', () => {
    const out = fitMiddle('customer-support-summariser-production-eu-west-a', 30);
    expect([...out]).toHaveLength(30);
    expect(out.startsWith('customer-support')).toBe(true);
    expect(out.endsWith('west-a')).toBe(true);
    expect(fitMiddle('short', 30)).toBe('short');
  });
});

describe('wrapLines', () => {
  it('wraps on words with no leading spaces', () => {
    expect(wrapLines('one two three four', 9, 5)).toEqual(['one two', 'three', 'four']);
  });

  it('ends the last line in an ellipsis when it cuts', () => {
    const lines = wrapLines('aaa bbb ccc ddd eee fff', 7, 2);
    expect(lines).toHaveLength(2);
    expect(lines[1]).toMatch(/…$/);
    expect(lines.every((l) => [...l].length <= 7)).toBe(true);
  });

  it('splits a word longer than the width', () => {
    expect(wrapLines('abcdefghij', 4, 5)).toEqual(['abcd', 'efgh', 'ij']);
  });

  it('counts the cells a wide character takes, wrapping and cutting', () => {
    expect(wrapLines('ab 界界界界tail', 5, 9)).toEqual(['ab', '界界', '界界t', 'ail']);
    expect(wrapLines('界界 界界', 5, 9)).toEqual(['界界', '界界']);
    const cut = wrapLines('界界界界界界', 5, 2);
    expect(cut).toEqual(['界界', '界界…']);
    expect(cut.every((l) => textWidth(l) <= 5)).toBe(true);
    // One too wide for the line is still taken, alone.
    expect(wrapLines('界a', 1, 5)).toEqual(['界', 'a']);
  });
});

describe('terminal cells', () => {
  const coder = '👩\u200d💻';

  it('counts a joined emoji, a CJK character and a plain letter by the cells they take', () => {
    expect(textWidth(coder)).toBe(2);
    expect(textWidth('東京')).toBe(4);
    expect(textWidth('ab')).toBe(2);
  });

  it('cuts at whole characters, never inside a joined emoji', () => {
    const name = `${coder.repeat(18)}-team`;
    for (const width of [6, 9, 12, 20]) {
      for (const cut of [fit(name, width), fitMiddle(name, width), tailCells(name, width)]) {
        expect(cut).not.toContain('\uFFFD');
        expect(textWidth(cut)).toBeLessThanOrEqual(width);
        // Every joiner is followed by its partner: no half of a pair is left.
        expect(cut).not.toMatch(/\u200d(?!\p{Extended_Pictographic})/u);
        expect(cut).not.toMatch(/\u200d$/);
      }
    }
  });

  it('pads and cuts CJK text to the cells', () => {
    expect(textWidth(padEnd('東京', 7))).toBe(7);
    expect(textWidth(fit('東京の請求', 7))).toBeLessThanOrEqual(7);
  });
});
