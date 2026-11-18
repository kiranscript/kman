// ink-testing-library with a terminal width we choose: its own stdout is fixed at 100
// columns, which would clip (and so hide) every layout wider than that.
import { EventEmitter } from 'node:events';
import { render as inkRender } from 'ink';
import type { ReactElement } from 'react';

class Out extends EventEmitter {
  frames: string[] = [];
  private last: string | undefined;
  constructor(readonly columns: number) {
    super();
  }
  write = (frame: string) => {
    this.frames.push(frame);
    this.last = frame;
  };
  lastFrame = () => this.last;
}

class In extends EventEmitter {
  isTTY = true;
  data: string | null = null;
  write = (data: string) => {
    this.data = data;
    this.emit('readable');
    this.emit('data', data);
  };
  setEncoding() {}
  setRawMode() {}
  resume() {}
  pause() {}
  ref() {}
  unref() {}
  read = () => {
    const { data } = this;
    this.data = null;
    return data;
  };
}

export function render(tree: ReactElement, columns = 100) {
  const stdout = new Out(columns);
  const stderr = new Out(columns);
  const stdin = new In();
  const instance = inkRender(tree, {
    stdout: stdout as never,
    stderr: stderr as never,
    stdin: stdin as never,
    debug: true,
    exitOnCtrlC: false,
    patchConsole: false,
  });
  return {
    rerender: instance.rerender,
    unmount: instance.unmount,
    stdin,
    lastFrame: stdout.lastFrame,
  };
}
