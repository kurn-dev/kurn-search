interface ConsumerWritableStream { write(chunk: string): boolean }
interface ConsumerProcess {
  readonly env: Readonly<Record<string, string | undefined>>;
  readonly stdout: ConsumerWritableStream;
  readonly stderr: ConsumerWritableStream;
  exit(code: number): never;
}
declare const process: ConsumerProcess;
