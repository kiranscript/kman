import { describe, expect, it } from 'vitest';
import { ago, bar, capMoney, fit, money, padEnd, parseDays, parseDollars, until } from '../src/format.js';

describe('money', () => {
  it('shows more decimals for small amounts', () => {
    expect(money(0)).toBe('$0');
    expect(money(36_000)).toBe('$0.036');
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
