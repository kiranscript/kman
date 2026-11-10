"""Drives a pinned official SDK through kman, for e2e/sdk_test.go.

Runs one scenario against KMAN_BASE_URL with KMAN_KEY, and prints what the SDK
made of the answer as JSON: {"text": ..., "usage": {...}}.
"""

import json
import os
import sys

import anthropic
import openai

base_url, key, scenario = os.environ["KMAN_BASE_URL"], os.environ["KMAN_KEY"], sys.argv[1]
oai = lambda: openai.OpenAI(base_url=base_url, api_key=key, max_retries=0, timeout=30)
ant = lambda: anthropic.Anthropic(base_url=base_url, api_key=key, max_retries=0, timeout=30)
messages = [{"role": "user", "content": "Say hello"}]


def openai_chat():
    r = oai().chat.completions.create(model="gpt-4o-mini", messages=messages)
    return r.choices[0].message.content, r.usage


def openai_chat_stream():
    text, usage = "", None
    for chunk in oai().chat.completions.create(model="gpt-4o-mini", messages=messages, stream=True):
        if chunk.choices:
            text += chunk.choices[0].delta.content or ""
        usage = chunk.usage or usage
    return text, usage


def openai_completions():
    r = oai().completions.create(model="gpt-3.5-turbo-instruct", prompt="Say hello")
    return r.choices[0].text, r.usage


def openai_completions_stream():
    text, usage = "", None
    for chunk in oai().completions.create(model="gpt-3.5-turbo-instruct", prompt="Say hello", stream=True):
        if chunk.choices:
            text += chunk.choices[0].text
        usage = chunk.usage or usage
    return text, usage


def openai_embeddings():
    r = oai().embeddings.create(model="text-embedding-3-small", input="Say hello")
    return ",".join(str(x) for x in r.data[0].embedding), r.usage


def openai_responses():
    r = oai().responses.create(model="gpt-5", input="Say hello")
    return r.output_text, r.usage


def openai_responses_stream():
    text, usage = "", None
    for event in oai().responses.create(model="gpt-5", input="Say hello", stream=True):
        if event.type == "response.output_text.delta":
            text += event.delta
        elif event.type == "response.completed":
            usage = event.response.usage
    return text, usage


def anthropic_messages():
    r = ant().messages.create(model="claude-sonnet-5-5", max_tokens=64, messages=messages)
    return r.content[0].text, r.usage


def anthropic_messages_stream():
    with ant().messages.stream(model="claude-sonnet-5-5", max_tokens=64, messages=messages) as stream:
        text = "".join(stream.text_stream)
        return text, stream.get_final_message().usage


def anthropic_error():
    try:
        ant().messages.create(model="claude-sonnet-5-5", max_tokens=64, messages=messages)
    except anthropic.RateLimitError as e:
        return e.body["error"]["type"], None
    raise SystemExit("the SDK did not raise its rate limit error")


text, usage = globals()[scenario.replace("-", "_")]()
print(json.dumps({"text": text, "usage": usage.model_dump() if usage else {}}))
