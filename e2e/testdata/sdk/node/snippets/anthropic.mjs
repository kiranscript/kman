import Anthropic from "@anthropic-ai/sdk";

// Reads ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL from the environment.
const client = new Anthropic();

const message = await client.messages.create({
  model: "claude-sonnet-5-5",
  max_tokens: 1024,
  messages: [{ role: "user", content: "Say hello" }],
});
console.log(message.content[0].text);
