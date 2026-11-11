import anthropic

# Reads ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL from the environment.
client = anthropic.Anthropic()

message = client.messages.create(
    model="claude-sonnet-5-5",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Say hello"}],
)
print(message.content[0].text)
