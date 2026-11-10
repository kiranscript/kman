// Drives a pinned official SDK through kman, for e2e/sdk_test.go.
//
// Runs one scenario against KMAN_BASE_URL with KMAN_KEY, and prints what the SDK
// made of the answer as JSON: {"text": ..., "usage": {...}}.
import Anthropic from "@anthropic-ai/sdk";
import OpenAI from "openai";

const baseURL = process.env.KMAN_BASE_URL;
const apiKey = process.env.KMAN_KEY;
const oai = () => new OpenAI({ baseURL, apiKey, maxRetries: 0, timeout: 30000 });
const ant = () => new Anthropic({ baseURL, apiKey, maxRetries: 0, timeout: 30000 });
const messages = [{ role: "user", content: "Say hello" }];

const scenarios = {
  async "openai-chat"() {
    const r = await oai().chat.completions.create({ model: "gpt-4o-mini", messages });
    return [r.choices[0].message.content, r.usage];
  },
  async "openai-chat-stream"() {
    let text = "", usage;
    for await (const chunk of await oai().chat.completions.create({ model: "gpt-4o-mini", messages, stream: true })) {
      if (chunk.choices.length) text += chunk.choices[0].delta.content ?? "";
      usage = chunk.usage ?? usage;
    }
    return [text, usage];
  },
  async "openai-completions"() {
    const r = await oai().completions.create({ model: "gpt-3.5-turbo-instruct", prompt: "Say hello" });
    return [r.choices[0].text, r.usage];
  },
  async "openai-completions-stream"() {
    let text = "", usage;
    for await (const chunk of await oai().completions.create({ model: "gpt-3.5-turbo-instruct", prompt: "Say hello", stream: true })) {
      if (chunk.choices.length) text += chunk.choices[0].text;
      usage = chunk.usage ?? usage;
    }
    return [text, usage];
  },
  async "openai-embeddings"() {
    const r = await oai().embeddings.create({ model: "text-embedding-3-small", input: "Say hello" });
    return [r.data[0].embedding.join(","), r.usage];
  },
  async "openai-responses"() {
    const r = await oai().responses.create({ model: "gpt-5", input: "Say hello" });
    return [r.output_text, r.usage];
  },
  async "openai-responses-stream"() {
    let text = "", usage;
    for await (const event of await oai().responses.create({ model: "gpt-5", input: "Say hello", stream: true })) {
      if (event.type === "response.output_text.delta") text += event.delta;
      if (event.type === "response.completed") usage = event.response.usage;
    }
    return [text, usage];
  },
  async "anthropic-messages"() {
    const r = await ant().messages.create({ model: "claude-sonnet-5-5", max_tokens: 64, messages });
    return [r.content[0].text, r.usage];
  },
  async "anthropic-messages-stream"() {
    const stream = ant().messages.stream({ model: "claude-sonnet-5-5", max_tokens: 64, messages });
    let text = "";
    stream.on("text", (delta) => { text += delta; });
    const final = await stream.finalMessage();
    return [text, final.usage];
  },
  async "anthropic-error"() {
    try {
      await ant().messages.create({ model: "claude-sonnet-5-5", max_tokens: 64, messages });
    } catch (e) {
      if (e instanceof Anthropic.RateLimitError) return [e.error.error.type, {}];
      throw e;
    }
    throw new Error("the SDK did not throw its rate limit error");
  },
};

const [text, usage] = await scenarios[process.argv[2]]();
console.log(JSON.stringify({ text, usage: usage ?? {} }));
