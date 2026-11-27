import { useRef } from 'react';

/**
 * Whether `ms` have passed since the screen last became `step`. Action keys are ignored
 * until then, so a key meant for what was there before does not act on what has just
 * appeared.
 */
export function useArmed(ms: number, step: string = ''): () => boolean {
  const since = useRef({ step, at: Date.now() });
  if (since.current.step !== step) since.current = { step, at: Date.now() };
  return () => Date.now() - since.current.at >= ms;
}
