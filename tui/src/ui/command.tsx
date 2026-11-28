import { Box, Text } from 'ink';

/** The command that does the same as the dialog it is in, wrapped: a cut one cannot be run. */
export function Command({ children, marginTop = 1 }: { children: string; marginTop?: number }) {
  return (
    <Box marginTop={marginTop} flexDirection="column">
      <Text dimColor>The same in a shell:</Text>
      <Text wrap="wrap">{children}</Text>
    </Box>
  );
}
