import OpenAI from "openai";

// Reads OPENAI_API_KEY and OPENAI_BASE_URL from the environment.
const client = new OpenAI();

const reply = await client.chat.completions.create({
  model: "gpt-4o-mini",
  messages: [{ role: "user", content: "Say hello" }],
});
console.log(reply.choices[0].message.content);
