import { useRef } from 'react';

/**
 * Whether `ms` have passed since the screen last became `step`. Action keys are ignored
 * until then, so a key meant for what was there before does not act on what has just
 * appeared. Timed by a clock that only goes forward: the wall clock can be stepped back,
 * which would leave keys ignored.
 */
export function useArmed(ms: number, step: string = ''): () => boolean {
  const since = useRef({ step, at: performance.now() });
  if (since.current.step !== step) since.current = { step, at: performance.now() };
  return () => performance.now() - since.current.at >= ms;
}
