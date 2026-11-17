import { Box, Text, useInput } from 'ink';
import { useState } from 'react';
import type { CreateKeyInput, KeyPolicy, Upstream } from '../api.js';
import { parseDays, parseDollars } from '../format.js';
import { editText, TextField } from '../ui/TextField.js';
import { Panel } from '../ui/parts.js';
import { color } from '../ui/theme.js';

const NAME_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;
const FIELDS = ['name', 'upstream', 'days', 'daily', 'monthly'] as const;
type Field = (typeof FIELDS)[number];
type TextFieldName = Exclude<Field, 'upstream'>;

const FIELD_W = 18;
const LABEL_W = 13;

/** What each text field accepts as it is typed, so a bad character never gets in. */
function clean(field: TextFieldName, value: string): string {
  switch (field) {
    case 'name':
      return value.toLowerCase().replace(/[^a-z0-9._-]/g, '');
    case 'days':
      return value.replace(/\D/g, '');
    default: {
      const v = value.replace(/[^\d.]/g, '');
      const dot = v.indexOf('.');
      return dot < 0 ? v : v.slice(0, dot + 1) + v.slice(dot + 1).replace(/\./g, '');
    }
  }
}

interface Props {
  upstreams: Upstream[];
  /** The install's default key lifetime, in days, when it is known. */
  defaultDays?: number;
  busy: boolean;
  error?: string;
  onSubmit: (input: CreateKeyInput) => void;
  onCancel: () => void;
}

export function CreateKeyDialog({ upstreams, defaultDays, busy, error, onSubmit, onCancel }: Props) {
  const [focus, setFocus] = useState(0);
  const [upstream, setUpstream] = useState(0);
  const [text, setText] = useState<Record<TextFieldName, string>>({ name: '', days: '', daily: '', monthly: '' });
  const [problem, setProblem] = useState<string | undefined>();
  const [discardArmed, setDiscardArmed] = useState(false);
  const current: Field = FIELDS[focus] ?? 'name';
  const touched = Object.values(text).some((v) => v !== '');

  const fail = (field: Field, message: string) => {
    setFocus(FIELDS.indexOf(field));
    setProblem(message);
  };

  function submit() {
    const up = upstreams[upstream];
    if (!up) return fail('upstream', 'Add an upstream first.');
    if (text.name && !NAME_RE.test(text.name)) {
      return fail('name', 'A name starts with a letter or digit.');
    }
    const policy: KeyPolicy = {};
    const input: CreateKeyInput = { upstream: up.name };
    if (text.name) input.name = text.name;
    if (text.days) {
      const d = parseDays(text.days);
      if (d === undefined) return fail('days', 'Expiry must be a whole number of days, at least 1.');
      input.ttl_seconds = d * 86_400;
    }
    if (text.daily) {
      const v = parseDollars(text.daily);
      if (v === undefined) return fail('daily', 'The daily cap is an amount in dollars above 0, such as 5.');
      policy.daily_cap_micro = v;
    }
    if (text.monthly) {
      const v = parseDollars(text.monthly);
      if (v === undefined) return fail('monthly', 'The monthly cap is an amount in dollars above 0, such as 50.');
      if (policy.daily_cap_micro !== undefined && v < policy.daily_cap_micro) {
        return fail('monthly', 'The monthly cap must be at least the daily cap.');
      }
      policy.monthly_cap_micro = v;
    }
    if (Object.keys(policy).length > 0) input.policy = policy;
    setProblem(undefined);
    onSubmit(input);
  }

  useInput((input, key) => {
    if (busy) return;
    if (key.escape) {
      // A half-filled form is not thrown away by one stray key.
      if (touched && !discardArmed) return setDiscardArmed(true);
      return onCancel();
    }
    setDiscardArmed(false);
    if (key.ctrl && input === 's') return submit();
    if (key.tab) {
      return setFocus((f) => (f + (key.shift ? FIELDS.length - 1 : 1)) % FIELDS.length);
    }
    if (key.downArrow) return setFocus((f) => Math.min(FIELDS.length - 1, f + 1));
    if (key.upArrow) return setFocus((f) => Math.max(0, f - 1));
    if (key.return) return focus < FIELDS.length - 1 ? setFocus(focus + 1) : submit();
    if (current === 'upstream') {
      if (key.leftArrow) setUpstream((u) => (u + upstreams.length - 1) % upstreams.length);
      if (key.rightArrow || input === ' ') setUpstream((u) => (u + 1) % upstreams.length);
      return;
    }
    const field = current;
    setText((t) => ({ ...t, [field]: clean(field, editText(t[field], input, key, field === 'name' ? 63 : 12)) }));
    setProblem(undefined);
  });

  if (upstreams.length === 0) {
    return (
      <Panel title="New virtual key" width={64}>
        <Box marginTop={1} flexDirection="column">
          <Text>A key belongs to an upstream, and there is none yet.</Text>
          <Text dimColor>Add one first: kman upstream add openai --preset openai</Text>
          <Box marginTop={1}>
            <Text dimColor>Esc to close</Text>
          </Box>
        </Box>
      </Panel>
    );
  }

  const up = upstreams[upstream];
  const noCredential = up !== undefined && up.credentials.length === 0;
  const label = (f: Field, name: string) => (
    <Text color={current === f ? color.accent : undefined} dimColor={current !== f}>
      {name.padEnd(LABEL_W)}
    </Text>
  );
  const row = (f: TextFieldName, name: string, hint: string, placeholder: string) => (
    <Box key={f}>
      {label(f, name)}
      <TextField value={text[f]} focused={current === f} width={FIELD_W} placeholder={placeholder} />
      <Text dimColor>  {hint}</Text>
    </Box>
  );
  const on = current === 'upstream';
  const edge = (c: string) => (
    <Text color={on ? color.accent : undefined} dimColor={!on}>
      {c}
    </Text>
  );

  return (
    <Panel title="New virtual key" width={72}>
      <Box flexDirection="column" marginTop={1}>
        {row('name', 'Name', 'optional; unnamed keys go by id', 'unnamed')}
        <Box>
          {label('upstream', 'Upstream')}
          {edge(on ? '‹' : '[')}
          <Text bold>{(up?.name ?? '').slice(0, FIELD_W).padEnd(FIELD_W)}</Text>
          {edge(on ? '›' : ']')}
          <Text dimColor>  {upstreams.length > 1 ? `${upstream + 1} of ${upstreams.length}, ← → to change` : 'your only upstream'}</Text>
        </Box>
        {row('days', 'Expires in', defaultDays ? `days (default ${defaultDays})` : 'days', 'default')}
        {row('daily', 'Daily cap', 'dollars', 'no cap')}
        {row('monthly', 'Monthly cap', 'dollars', 'no cap')}
      </Box>
      <Box marginTop={1} flexDirection="column">
        {noCredential ? (
          <Text color={color.warn}>▲ {up?.name} has no credential yet, so kman will refuse to create a key on it.</Text>
        ) : !text.daily && !text.monthly ? (
          <Text color={color.warn}>▲ With no cap, a leaked key can spend without limit.</Text>
        ) : (
          <Text> </Text>
        )}
        {problem || error ? (
          <Text color={color.danger} wrap="truncate-end">
            ✖ {problem ?? error}
          </Text>
        ) : (
          <Text> </Text>
        )}
        <Text color={discardArmed ? color.warn : undefined} dimColor={!discardArmed}>
          {busy
            ? 'Creating…'
            : discardArmed
              ? 'Esc again discards · any other key keeps editing'
              : 'Enter next (the last field creates) · Ctrl+S create now · Esc cancel'}
        </Text>
      </Box>
    </Panel>
  );
}
