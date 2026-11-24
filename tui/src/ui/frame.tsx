// The panel every screen is made of, drawn the way lazygit draws its views: the
// title and a count sit in the top border, the position ("2 of 7") in the bottom
// one, and only the focused panel has a bright border. Ink cannot put text in a
// border, so the top and bottom edges are drawn here and Ink draws the sides.
import { Box, Text } from 'ink';
import type { ReactNode } from 'react';
import { fit } from '../format.js';
import { color, noColor } from './theme.js';

const EDGES = {
  round: { tl: '╭', tr: '╮', bl: '╰', br: '╯', h: '─' },
  // The focused panel without colour: a heavy border still tells it apart.
  bold: { tl: '┏', tr: '┓', bl: '┗', br: '┛', h: '━' },
} as const;

interface FrameProps {
  /** "[2] Keys": the number is the key that jumps here. */
  title?: string;
  /** Shown after the title in the top border: "(3)". */
  count?: string;
  /** The right end of the bottom border: "2 of 7". */
  position?: string;
  /** Dim text at the right end of the top border, shown only if it fits beside the title. */
  subtitle?: string;
  /** The focused panel: green and bold. Others are muted. */
  focused?: boolean;
  /** Overrides the border colour, for a warning or a search. */
  tone?: string;
  /** Draw the heavy border of a focused panel without colour. Defaults to NO_COLOR being set. */
  heavy?: boolean;
  width: number;
  height?: number;
  children: ReactNode;
}

/** Whether a panel `width` wide shows the whole of this title and count, or cuts it. */
export function titleFits(title: string, count: string | undefined, width: number): boolean {
  return [...` ${title}${count ? ` ${count}` : ''} `].length <= Math.max(0, width - 4);
}

/** Whether `subtitle` fits in the top border of a panel `width` wide, beside its title and count. */
export function subtitleFits(subtitle: string, title: string, count: string | undefined, width: number): boolean {
  const label = [...` ${title}${count ? ` ${count}` : ''} `].length;
  return [...subtitle].length + 2 + Math.min(label, Math.max(0, width - 4)) + 3 <= width;
}

/** The width a strip's label takes: its title and count, and a space. */
export function stripLabelWidth(title: string, count?: string): number {
  return [...` ${title}${count ? ` ${count}` : ''} `].length;
}

/**
 * A panel squeezed to one line with no border: its title, then what its selected line
 * would have shown. This is how the inactive panels fit when the terminal is short.
 */
export function Strip({ title, count, width, children }: { title: string; count?: string; width: number; children: ReactNode }) {
  return (
    <Box width={width} height={1} flexShrink={0} overflow="hidden">
      <Text bold dimColor>
        {` ${title}${count ? ` ${count}` : ''} `}
      </Text>
      <Box flexDirection="column" flexGrow={1} overflow="hidden">
        {children}
      </Box>
    </Box>
  );
}

export function Frame({ title, count, position, subtitle, focused, tone, heavy = noColor, width, height, children }: FrameProps) {
  const edge = EDGES[focused && heavy ? 'bold' : 'round'];
  const border = tone ?? (focused ? color.ok : undefined);
  const dim = !focused && tone === undefined;
  const style = { color: border, dimColor: dim, bold: focused };

  const label = title ? ` ${title}${count ? ` ${count}` : ''} ` : '';
  const shownLabel = fit(label, Math.max(0, width - 4));
  const sub = subtitle ? ` ${subtitle} ` : '';
  const showSub = sub !== '' && [...sub].length + [...shownLabel].length + 3 <= width;
  const topFill = Math.max(0, width - 3 - [...shownLabel].length - (showSub ? [...sub].length : 0));
  const pos = position ? ` ${position} ` : '';
  const shownPos = fit(pos, Math.max(0, width - 4));
  const bottomFill = Math.max(0, width - 3 - [...shownPos].length);

  return (
    <Box flexDirection="column" width={width} height={height} flexShrink={0}>
      <Box width={width}>
        <Text {...style}>{edge.tl + edge.h}</Text>
        <Text color={border} bold={focused || title !== undefined} dimColor={dim && !title}>
          {shownLabel}
        </Text>
        <Text {...style}>{edge.h.repeat(topFill)}</Text>
        {showSub ? <Text dimColor>{sub}</Text> : null}
        <Text {...style}>{edge.tr}</Text>
      </Box>
      <Box
        flexDirection="column"
        flexGrow={1}
        width={width}
        borderStyle={focused && heavy ? 'bold' : 'round'}
        borderTop={false}
        borderBottom={false}
        borderColor={border}
        borderDimColor={dim}
        overflow="hidden"
      >
        {children}
      </Box>
      <Box width={width}>
        <Text {...style}>{edge.bl + edge.h.repeat(bottomFill)}</Text>
        <Text color={border} dimColor={dim}>
          {shownPos}
        </Text>
        <Text {...style}>{edge.h + edge.br}</Text>
      </Box>
    </Box>
  );
}
