import { describe, expect, it } from 'vitest';
import type { Key } from '../src/api.js';
import { upstream } from './fakes.js';
import { keyRisks, upstreamAttention, upstreamRisks } from '../src/risk.js';

const now = Date.parse('2026-11-17T12:00:00Z');
const day = 86_400_000;

function key(over: Partial<Key> = {}): Key {
  return {
    id: 'k1',
    name: 'k',
    status: 'active',
    expired: false,
    tags: [],
    policy: {},
    created_at: '2026-11-01T00:00:00Z',
    expires_at: new Date(now + 60 * day).toISOString(),
    spend: {
      today: { period: '2026-11-17', cost_micro: 0, requests: 0, cap_micro: 1_000_000 },
      month: { period: '2026-11', cost_micro: 0, requests: 0, cap_micro: 10_000_000 },
    },
    ...over,
  };
}

describe('keyRisks', () => {
  it('has none for a capped, healthy key', () => {
    expect(keyRisks(key(), now)).toEqual([]);
  });
  it('says so when there is no cap at all', () => {
    const k = key({ spend: { today: { period: 'd', cost_micro: 0, requests: 0 }, month: { period: 'm', cost_micro: 0, requests: 0 } } });
    expect(keyRisks(k, now)).toMatchObject([{ severity: 'info', label: 'no spend cap' }]);
  });
  it('warns near a cap and again at it', () => {
    const near = key();
    near.spend!.today.cost_micro = 850_000;
    expect(keyRisks(near, now)).toMatchObject([{ severity: 'warn', label: '85% of its daily cap' }]);
    const at = key();
    at.spend!.today.cost_micro = 1_000_000;
    expect(keyRisks(at, now)[0]).toMatchObject({ severity: 'danger', label: 'at its daily cap' });
  });
  it('counts what is reserved for requests in flight', () => {
    const k = key();
    k.spend!.today.cost_micro = 500_000;
    k.spend!.today.reserved_micro = 400_000;
    expect(keyRisks(k, now)[0]?.label).toBe('90% of its daily cap');
  });
  it('warns before expiry and flags an expired key', () => {
    expect(keyRisks(key({ expires_at: new Date(now + 3.5 * day).toISOString() }), now)[0]).toMatchObject({ severity: 'warn', label: 'expires in 4 days' });
    expect(keyRisks(key({ expires_at: new Date(now + 5 * 3_600_000).toISOString() }), now)[0]?.label).toBe('expires within a day');
    expect(keyRisks(key({ expired: true }), now)[0]).toMatchObject({ severity: 'danger', label: 'expired' });
  });
  it('flags unmetered requests', () => {
    const k = key();
    k.spend!.today.unmetered_requests = 2;
    expect(keyRisks(k, now).map((r) => r.label)).toContain('2 unmetered today');
  });
  it('has none for a key that is already disabled or revoked', () => {
    expect(keyRisks(key({ status: 'disabled', expired: true }), now)).toEqual([]);
    expect(keyRisks(key({ status: 'revoked' }), now)).toEqual([]);
  });
});

describe('attention order', () => {
  it('puts the sooner expiry and the fuller cap first within one severity', async () => {
    const { attention } = await import('../src/risk.js');
    const soon = key({ id: 'soon', name: 'soon', expires_at: new Date(now + 5 * 3_600_000).toISOString() });
    const later = key({ id: 'later', name: 'later', expires_at: new Date(now + 3 * day).toISOString() });
    const full = key({ id: 'full', name: 'full' });
    full.spend!.today.cost_micro = 950_000;
    const half = key({ id: 'half', name: 'half' });
    half.spend!.today.cost_micro = 810_000;
    const names = attention([later, half, full, soon], now).map((a) => a.key.name);
    expect(names.indexOf('soon')).toBeLessThan(names.indexOf('later'));
    expect(names.indexOf('full')).toBeLessThan(names.indexOf('half'));
  });
});

it('keeps local plaintext visible on the upstream without crowding out actionable warnings', () => {
  for (const host of ['127.0.0.1', '127.1.2.3', '::1', '0:0:0:0:0:0:0:1', 'localhost.']) {
    const u = { ...upstream, host, tls: false };
    expect(upstreamRisks(u).map((r) => r.short)).toContain('plain http');
    expect(upstreamAttention([u])).toEqual([]);
    expect(upstreamAttention([{ ...u, credentials: [] }])[0]?.risks.map((r) => r.short)).toEqual(['no credential']);
  }
  expect(upstreamAttention([{ ...upstream, tls: false, host: '127.0.0.1.example' }])).toHaveLength(1);
});
