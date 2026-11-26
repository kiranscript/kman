// A loopback upstream for checking a copied virtual key without a provider account.
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';

const fixture = new URL('../../../e2e/testdata/transcripts/openai-chat.json', import.meta.url);
const { request, response } = JSON.parse(await readFile(fixture, 'utf8')).exchanges[0];

const server = createServer((req, res) => {
  if (req.method !== request.method || req.url !== request.path) {
    res.writeHead(404).end();
    return;
  }
  if (req.headers.authorization !== 'Bearer usability-only') {
    res.writeHead(401).end();
    return;
  }
  res.writeHead(response.status, response.headers).end(response.chunks.join(''));
});

server.listen(0, '127.0.0.1', () => {
  console.log(`Mock upstream listening on 127.0.0.1:${server.address().port}`);
});
