import { describe, expect, it } from 'vitest';
import type { Key } from '../src/api.js';
import { incident, upstream } from './fakes.js';
import { attention, keyRisks, otherIncidents, topRisk, upstreamAttention, upstreamRisks } from '../src/risk.js';

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

describe('who and where come first', () => {
  const near = () => {
    const k = key({ id: 'k_near', name: 'near' });
    k.spend!.today.cost_micro = 950_000;
    return k;
  };

  it('puts an open incident ahead of every other risk, even a contained key', () => {
    const k = near();
    const watch = { incidents: [incident({ id: 4, key_id: 'k_near' })] };
    expect(topRisk(keyRisks(k, now, watch))).toMatchObject({ severity: 'danger', short: 'incident', fix: 'kman incident show 4' });
    // A disabled key is contained, but its incident is still a case to answer.
    expect(keyRisks({ ...k, status: 'disabled' }, now, watch).map((r) => r.short)).toEqual(['incident']);
    // Resolved is over.
    expect(keyRisks(k, now, { incidents: [incident({ id: 4, key_id: 'k_near', state: 'resolved' })] }).map((r) => r.short)).not.toContain('incident');
  });

  it('marks an acknowledged or low incident as a warning, and a canary as used', () => {
    expect(keyRisks(key(), now, { incidents: [incident({ id: 1, key_id: 'k1', state: 'acknowledged' })] })[0]).toMatchObject({ severity: 'warn' });
    expect(keyRisks(key(), now, { incidents: [incident({ id: 1, key_id: 'k1', severity: 'low' })] })[0]).toMatchObject({ severity: 'warn' });
    expect(keyRisks(key(), now, { incidents: [incident({ id: 1, key_id: 'k1', rules: ['canary'] })] })[0]).toMatchObject({ short: 'canary used', listLabel: 'canary used' });
  });

  it('ranks a key open to every address, or unused, above one near its cap', () => {
    const open = key({ id: 'k_open', name: 'open' });
    const idle = key({ id: 'k_idle', name: 'idle' });
    const findings = [
      { key_id: 'k_open', rule: 'open_to_every_address', message: '', fix: 'kman key update open' },
      { key_id: 'k_idle', rule: 'unused', message: '', fix: 'kman key disable idle' },
    ];
    expect(attention([near(), idle, open], now, { findings }).map((a) => a.key.name)).toEqual(['open', 'idle', 'near']);
  });

  it('ranks any unresolved incident above red spend, expiry and a missing credential', () => {
    const capped = key({ id: 'capped', name: 'capped' });
    capped.spend!.today.cost_micro = 1_000_000;
    const expired = key({ id: 'expired', name: 'expired', expired: true });
    const low = key({ id: 'low', name: 'low' });
    const acked = key({ id: 'acked', name: 'acked' });
    const hot = key({ id: 'hot', name: 'hot' });
    const watch = {
      incidents: [
        incident({ id: 1, key_id: 'low', severity: 'low' }),
        incident({ id: 2, key_id: 'acked', severity: 'critical', state: 'acknowledged' }),
        incident({ id: 3, key_id: 'hot', severity: 'high' }),
      ],
    };
    const needs = attention([capped, expired, low, acked, hot], now, watch);
    // Red incidents first, then the others, then what is red for another reason.
    expect(needs.map((a) => a.key.id)).toEqual(['hot', 'low', 'acked', 'expired', 'capped']);
    const both = keyRisks({ ...capped, id: 'low' }, now, watch);
    expect(topRisk(both)?.short).toBe('incident');
  });

  it('puts any expiry ahead of a cap nearly reached', () => {
    const full = near();
    full.spend!.today.cost_micro = 999_000;
    const soon = key({ id: 'soon', name: 'soon', expires_at: new Date(now + 6.9 * day).toISOString() });
    expect(attention([full, soon], now).map((a) => a.key.id)).toEqual(['soon', 'k_near']);
  });

  it('finds the incidents whose key is not in the list: canaries', () => {
    const list = [incident({ id: 1, key_id: 'k1' }), incident({ id: 2, key_id: 'k_canary', rules: ['canary'] })];
    expect(otherIncidents([key()], list).map((i) => i.id)).toEqual([2]);
  });
});
