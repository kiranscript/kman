// A demo of the TUI against the in-memory fake admin API: no kman server needed.
//   cd tui && npx tsx test/demo.tsx
import { render } from 'ink';
import { App } from '../src/App.js';
import { fakeClient, key } from './fakes.js';

const client = fakeClient();
const day = 86_400_000;
client.keys.push(
  key({ id: 'k_prod', name: 'prod-api', spend: { today: { period: 'd', cost_micro: 4_300_000, requests: 812, cap_micro: 5_000_000 }, month: { period: 'm', cost_micro: 61_000_000, requests: 9100, cap_micro: 200_000_000 } } }),
  key({ id: 'k_soon', name: 'contractor', expires_at: new Date(Date.now() + 3 * day).toISOString() }),
  key({ id: 'k_nocap', name: 'scratch', spend: undefined }),
  key({ id: 'k_rev', name: 'leaked-demo', status: 'revoked' }),
);
render(<App client={client} url="http://127.0.0.1:8081" intervalMs={3000} />);
