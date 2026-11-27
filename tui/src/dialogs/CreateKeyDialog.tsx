import { Box, Text, useInput } from 'ink';
import { useState } from 'react';
import type { CreateKeyInput, KeyPolicy, Upstream } from '../api.js';
import { fitMiddle, padEnd, parseDays, parseDollars, wrapLines } from '../format.js';
import { editField, TextField } from '../ui/TextField.js';
import { Panel, UpstreamSetup, Wrapped } from '../ui/parts.js';
import { color, severityGlyph } from '../ui/theme.js';

const NAME_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;
type Field = 'name' | 'upstream' | 'credential' | 'days' | 'daily' | 'monthly';
type TextFieldName = Exclude<Field, 'upstream' | 'credential'>;

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
  /** The most lines a server error may take, which is as many as the screen has room for. */
  errorLines?: number;
  onSubmit: (input: CreateKeyInput) => void;
  onCancel: () => void;
}

export function CreateKeyDialog({ upstreams, defaultDays, busy, error, errorLines = 3, onSubmit, onCancel }: Props) {
  const [focus, setFocus] = useState(0);
  // Chosen by name, not by position: the list is re-read every few seconds and may
  // change under an open form.
  const [upstreamName, setUpstreamName] = useState<string | undefined>();
  const [credentialName, setCredentialName] = useState<string | undefined>();
  const [text, setText] = useState<Record<TextFieldName, string>>({ name: '', days: '', daily: '', monthly: '' });
  const [cursor, setCursor] = useState<Record<TextFieldName, number>>({ name: 0, days: 0, daily: 0, monthly: 0 });
  const [problem, setProblem] = useState<string | undefined>();
  const [discardArmed, setDiscardArmed] = useState(false);
  const upstream = Math.max(0, upstreams.findIndex((u) => u.name === upstreamName));
  const up = upstreams[upstream];
  const credentials = up?.credentials ?? [];
  // An upstream with several credentials needs one named; with one, it is the one.
  const choosesCredential = credentials.length > 1;
  const credential = Math.max(0, credentials.findIndex((c) => c.name === credentialName));
  const fields: Field[] = ['name', 'upstream', ...(choosesCredential ? (['credential'] as const) : []), 'days', 'daily', 'monthly'];
  const current: Field = fields[Math.min(focus, fields.length - 1)] ?? 'name';
  const touched = Object.values(text).some((v) => v !== '');

  const fail = (field: Field, message: string) => {
    setFocus(fields.indexOf(field));
    setProblem(message);
  };

  function submit() {
    if (!up) return fail('upstream', 'Add an upstream first.');
    if (text.name && !NAME_RE.test(text.name)) {
      return fail('name', 'A name starts with a letter or digit.');
    }
    const policy: KeyPolicy = {};
    const input: CreateKeyInput = { upstream: up.name };
    if (choosesCredential) input.credential = credentials[credential]?.name;
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
      return setFocus((f) => (f + (key.shift ? fields.length - 1 : 1)) % fields.length);
    }
    if (key.downArrow) return setFocus((f) => Math.min(fields.length - 1, f + 1));
    if (key.upArrow) return setFocus((f) => Math.max(0, f - 1));
    if (key.return) return focus < fields.length - 1 ? setFocus(focus + 1) : submit();
    if (current === 'upstream' || current === 'credential') {
      const back = key.leftArrow;
      const forward = key.rightArrow || input === ' ';
      if (!back && !forward) return;
      const step = (at: number, n: number) => (at + (back ? n - 1 : 1)) % n;
      if (current === 'upstream') {
        setUpstreamName(upstreams[step(upstream, upstreams.length)]?.name);
        setCredentialName(undefined);
      } else {
        setCredentialName(credentials[step(credential, credentials.length)]?.name);
      }
      return;
    }
    const field = current;
    const next = editField({ value: text[field], cursor: cursor[field] }, input, key, field === 'name' ? 63 : 12, (typed) => clean(field, typed));
    // A decimal may not gain a second point; what that removes comes off the cursor's left too.
    const value = clean(field, next.value);
    setText((t) => ({ ...t, [field]: value }));
    setCursor((c) => ({ ...c, [field]: Math.min(next.cursor, [...value].length) }));
    setProblem(undefined);
  });

  if (upstreams.length === 0) {
    return (
      <Panel title="New virtual key" width={64}>
        <Box flexDirection="column">
          <Text>A key belongs to an upstream, and there is none yet.</Text>
          <UpstreamSetup />
        </Box>
      </Panel>
    );
  }

  const noCredential = up !== undefined && up.credentials.length === 0;
  const warning = `${up?.name} has no credential yet, so kman will refuse to create a key on it.`;
  // Rows the form has beyond its usual 12, which come out of what a server error may take.
  const extraRows = (choosesCredential ? 1 : 0) + (noCredential ? wrapLines(warning, 62, 2).length - 1 : 0);
  const label = (f: Field, name: string) => (
    <Text color={current === f ? color.accent : undefined} dimColor={current !== f}>
      {name.padEnd(LABEL_W)}
    </Text>
  );
  const row = (f: TextFieldName, name: string, hint: string, placeholder: string) => (
    <Box key={f}>
      {label(f, name)}
      <TextField value={text[f]} cursor={cursor[f]} focused={current === f} width={FIELD_W} placeholder={placeholder} />
      <Text dimColor>  {hint}</Text>
    </Box>
  );
  /** A value chosen with ← →, in brackets that turn to arrows while it has focus. */
  const picker = (f: 'upstream' | 'credential', name: string, value: string, hint: string) => {
    const on = current === f;
    const edge = (c: string) => (
      <Text color={on ? color.accent : undefined} dimColor={!on}>
        {c}
      </Text>
    );
    return (
      <Box>
        {label(f, name)}
        {edge(on ? '‹' : '[')}
        <Text bold>{padEnd(fitMiddle(value, FIELD_W), FIELD_W)}</Text>
        {edge(on ? '›' : ']')}
        <Text dimColor>  {hint}</Text>
      </Box>
    );
  };

  return (
    <Panel title="New virtual key" width={72}>
      <Box flexDirection="column">
        {row('name', 'Name', 'optional; unnamed keys go by id', 'unnamed')}
        {picker('upstream', 'Upstream', up?.name ?? '', upstreams.length > 1 ? `${upstream + 1} of ${upstreams.length}, ← → to change` : 'your only upstream')}
        {choosesCredential &&
          picker('credential', 'Credential', credentials[credential]?.name ?? '', `${credential + 1} of ${credentials.length}, ← → to change`)}
        {row('days', 'Expires in', defaultDays ? `days (default ${defaultDays})` : 'days', 'default')}
        {row('daily', 'Daily cap', 'dollars', 'no cap')}
        {row('monthly', 'Monthly cap', 'dollars', 'no cap')}
      </Box>
      <Box marginTop={1} flexDirection="column">
        {noCredential ? (
          <Wrapped text={warning} marker={`${severityGlyph.warn} `} width={64} maxLines={2} tone={color.warn} />
        ) : !text.daily && !text.monthly ? (
          <Text color={color.warn}>{severityGlyph.warn} With no cap, a leaked key can spend without limit.</Text>
        ) : null}
        {problem || error ? (
          <Wrapped text={problem ?? error ?? ''} marker={`${severityGlyph.danger} `} width={64} maxLines={Math.max(3, errorLines - extraRows)} tone={color.danger} />
        ) : null}
        {busy || discardArmed ? (
          <Text color={discardArmed ? color.warn : undefined} dimColor={!discardArmed}>
            {busy ? 'Creating…' : 'Esc again discards · any other key keeps editing'}
          </Text>
        ) : null}
      </Box>
    </Panel>
  );
}
