// Minimal ambient typing for the few Node.js globals this consumer uses. The
// example deliberately adds no @types/node or other dependency; only the
// private client package is installed.
interface ConsumerWritableStream {
  write(chunk: string): boolean;
}

interface ConsumerProcess {
  readonly env: Readonly<Record<string, string | undefined>>;
  readonly stdout: ConsumerWritableStream;
  readonly stderr: ConsumerWritableStream;
  exit(code: number): never;
}

declare const process: ConsumerProcess;
