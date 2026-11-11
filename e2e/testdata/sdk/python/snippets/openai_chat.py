from openai import OpenAI

# Reads OPENAI_API_KEY and OPENAI_BASE_URL from the environment.
client = OpenAI()

reply = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Say hello"}],
)
print(reply.choices[0].message.content)
