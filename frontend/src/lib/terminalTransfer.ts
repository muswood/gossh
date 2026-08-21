// owner: muswood | Email: mumu920@outlook.com
export type BinaryWriter = (bytes: Uint8Array) => Promise<void>;
export type TransferProgress = (done: number, total: number) => void;
export type TransferVisible = (bytes: Uint8Array) => void;

const SOH = 0x01;
const STX = 0x02;
const EOT = 0x04;
const ACK = 0x06;
const NAK = 0x15;
const CAN = 0x18;
const CRC = 0x43;
const PAD = 0x1a;

function isProtocolControl(value: number): boolean {
  return value === SOH || value === STX || value === EOT || value === ACK || value === NAK || value === CAN;
}

export interface TransferResult { name: string; data: Uint8Array; }

function crc16(data: Uint8Array): number {
  let crc = 0;
  for (const value of data) {
    crc ^= value << 8;
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc & 0x8000) ? ((crc << 1) ^ 0x1021) & 0xffff : (crc << 1) & 0xffff;
    }
  }
  return crc;
}

function concat(left: Uint8Array, right: Uint8Array): Uint8Array {
  const result = new Uint8Array(left.length + right.length);
  result.set(left);
  result.set(right, left.length);
  return result;
}

function concatMany(chunks: Uint8Array[]): Uint8Array {
  return chunks.reduce((all, chunk) => concat(all, chunk), new Uint8Array(0));
}

function packet(type: number, sequence: number, payload: Uint8Array): Uint8Array {
  const result = new Uint8Array(3 + payload.length + 2);
  result[0] = type;
  result[1] = sequence & 0xff;
  result[2] = 0xff - (sequence & 0xff);
  result.set(payload, 3);
  const checksum = crc16(payload);
  result[result.length - 2] = checksum >> 8;
  result[result.length - 1] = checksum & 0xff;
  return result;
}

function paddedPayload(data: Uint8Array, offset: number, size: number, fill = PAD): { payload: Uint8Array; sent: number } {
  const source = data.slice(offset, offset + size);
  const payload = new Uint8Array(size);
  payload.fill(fill);
  payload.set(source);
  return { payload, sent: source.length };
}

function parseIncomingPacket(input: Uint8Array): { frame?: Uint8Array; type?: number; size?: number; wait?: boolean } {
  if (!input.length) return { wait: true };
  const type = input[0];
  if (type !== SOH && type !== STX) return {};
  const size = type === STX ? 1024 : 128;
  const length = size + 5;
  if (input.length < length) return { wait: true };
  return { frame: input.slice(0, length), type, size };
}

function isValidPacket(frame: Uint8Array, size: number): boolean {
  const sequence = frame[1];
  const payload = frame.slice(3, 3 + size);
  const actual = (frame[size + 3] << 8) | frame[size + 4];
  return ((sequence + frame[2]) & 0xff) === 0xff && crc16(payload) === actual;
}

function previousSequence(sequence: number): number {
  return (sequence + 255) & 0xff;
}

function safeName(name: string): string {
  return (name.split(/[\\/]/).pop() || name || "download.bin").replace(/\0/g, "");
}

function encodeAscii(value: string): Uint8Array {
  const result = new Uint8Array(value.length);
  for (let i = 0; i < value.length; i++) result[i] = value.charCodeAt(i) & 0x7f;
  return result;
}

function ymodemHeaderPayload(name: string, size: number): Uint8Array {
  const payload = new Uint8Array(128);
  const metadata = encodeAscii(`${safeName(name)}\0${size}\0`);
  payload.set(metadata.slice(0, payload.length));
  return payload;
}

function readNullTerminated(bytes: Uint8Array, start = 0): { value: string; next: number } {
  let end = start;
  while (end < bytes.length && bytes[end] !== 0) end++;
  let value = "";
  for (let i = start; i < end; i++) value += String.fromCharCode(bytes[i]);
  return { value, next: Math.min(end + 1, bytes.length) };
}

abstract class TransferBase {
  protected input: Uint8Array = new Uint8Array(0);
  protected stopped = false;
  private visibleChunks: Uint8Array[] = [];

  constructor(
    protected readonly write: BinaryWriter,
    protected readonly progress: TransferProgress,
    protected readonly done: (result?: TransferResult, error?: Error) => void,
    private readonly onVisible?: TransferVisible,
  ) {}

  protected append(data: Uint8Array) {
    this.input = concat(this.input, data);
  }

  protected finish(result?: TransferResult, error?: Error) {
    if (this.stopped) return;
    this.stopped = true;
    this.done(result, error);
  }

  protected showVisible(data: Uint8Array) {
    if (data.length) this.visibleChunks.push(data.slice());
  }

  protected showVisibleByte(value: number) {
    this.showVisible(new Uint8Array([value]));
  }

  protected flushVisible() {
    if (!this.visibleChunks.length) return;
    const visible = concatMany(this.visibleChunks);
    this.visibleChunks = [];
    this.onVisible?.(visible);
  }

  cancel() {
    if (this.stopped) return;
    this.stopped = true;
    void this.write(new Uint8Array([CAN, CAN, CAN]));
    this.done(undefined, new Error("传输已取消"));
  }

  abstract push(data: Uint8Array): void;
}

export class XModemSender extends TransferBase {
  private offset = 0;
  private sequence = 1;
  private currentPacket: Uint8Array = new Uint8Array(0);
  private state: "start" | "ack" | "eot" | "done" = "start";
  private sending = false;

  constructor(
    write: BinaryWriter,
    private readonly data: Uint8Array,
    _name: string,
    progress: TransferProgress,
    done: (result?: TransferResult, error?: Error) => void,
    onVisible?: TransferVisible,
  ) {
    super(write, progress, done, onVisible);
  }

  push(data: Uint8Array) {
    this.append(data);
    while (this.input.length && !this.stopped) {
      const control = this.input[0];
      this.input = this.input.slice(1);
      if (control === CAN) {
        this.finish(undefined, new Error("远端取消传输"));
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      if (this.state === "start" && (control === CRC || control === NAK)) {
        this.state = "ack";
        void this.sendNextBlock();
        continue;
      }
      if (this.state === "ack" && control === ACK) {
        if (this.offset >= this.data.length) {
          this.state = "eot";
          this.currentPacket = new Uint8Array(0);
          void this.write(new Uint8Array([EOT]));
        } else {
          void this.sendNextBlock();
        }
        continue;
      }
      if (this.state === "ack" && control === NAK && this.currentPacket.length) {
        void this.write(this.currentPacket);
        continue;
      }
      if (this.state === "eot" && control === ACK) {
        this.state = "done";
        this.finish();
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      if (control !== CRC && control !== NAK && control !== ACK) this.showVisibleByte(control);
    }
    this.flushVisible();
  }

  private async sendNextBlock() {
    if (this.sending || this.stopped) return;
    this.sending = true;
    if (this.offset >= this.data.length) {
      this.state = "eot";
      this.currentPacket = new Uint8Array(0);
      await this.write(new Uint8Array([EOT]));
      this.sending = false;
      return;
    }
    const { payload, sent } = paddedPayload(this.data, this.offset, 128);
    this.currentPacket = packet(SOH, this.sequence, payload);
    this.offset += sent;
    await this.write(this.currentPacket);
    this.sequence = (this.sequence + 1) & 0xff;
    this.progress(Math.min(this.offset, this.data.length), this.data.length);
    this.sending = false;
  }
}

export class XModemReceiver extends TransferBase {
  private expected = 1;
  private chunks: Uint8Array[] = [];
  private received = 0;

  constructor(
    write: BinaryWriter,
    private readonly name: string,
    progress: TransferProgress,
    done: (result?: TransferResult, error?: Error) => void,
    onVisible?: TransferVisible,
  ) {
    super(write, progress, done, onVisible);
    void write(new Uint8Array([CRC]));
  }

  push(data: Uint8Array) {
    this.append(data);
    while (!this.stopped && this.input.length) {
      if (this.input[0] === CAN) {
        this.input = this.input.slice(1);
        this.finish(undefined, new Error("远端取消传输"));
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      if (this.input[0] === EOT) {
        this.input = this.input.slice(1);
        void this.write(new Uint8Array([ACK]));
        this.finish({ name: this.name, data: concatMany(this.chunks) });
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      const parsed = parseIncomingPacket(this.input);
      if (parsed.wait) {
        this.flushVisible();
        return;
      }
      if (!parsed.frame || !parsed.size) {
        if (!isProtocolControl(this.input[0])) this.showVisibleByte(this.input[0]);
        this.input = this.input.slice(1);
        continue;
      }
      this.input = this.input.slice(parsed.frame.length);
      if (!isValidPacket(parsed.frame, parsed.size)) {
        void this.write(new Uint8Array([NAK]));
        continue;
      }
      const sequence = parsed.frame[1];
      if (sequence === this.expected) {
        const payload = parsed.frame.slice(3, 3 + parsed.size);
        this.chunks.push(payload);
        this.received += payload.length;
        this.expected = (this.expected + 1) & 0xff;
        this.progress(this.received, 0);
      } else if (sequence !== previousSequence(this.expected)) {
        void this.write(new Uint8Array([NAK]));
        continue;
      }
      void this.write(new Uint8Array([ACK]));
    }
    this.flushVisible();
  }
}

export class YModemSender extends TransferBase {
  private offset = 0;
  private sequence = 1;
  private currentPacket: Uint8Array = new Uint8Array(0);
  private state: "header" | "headerAck" | "data" | "eotNak" | "eotAck" | "endHeader" | "done" = "header";
  private sending = false;

  constructor(
    write: BinaryWriter,
    private readonly data: Uint8Array,
    private readonly name: string,
    progress: TransferProgress,
    done: (result?: TransferResult, error?: Error) => void,
    onVisible?: TransferVisible,
  ) {
    super(write, progress, done, onVisible);
  }

  push(data: Uint8Array) {
    this.append(data);
    while (this.input.length && !this.stopped) {
      const control = this.input[0];
      this.input = this.input.slice(1);
      if (control === CAN) {
        this.finish(undefined, new Error("远端取消传输"));
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      if (this.state === "header" && (control === CRC || control === NAK)) {
        this.currentPacket = packet(SOH, 0, ymodemHeaderPayload(this.name, this.data.length));
        this.state = "headerAck";
        void this.write(this.currentPacket);
        continue;
      }
      if (this.state === "headerAck" && control === NAK) {
        void this.write(this.currentPacket);
        continue;
      }
      if (this.state === "headerAck" && control === ACK) {
        continue;
      }
      if (this.state === "headerAck" && control === CRC) {
        this.state = "data";
        void this.sendNextDataBlock();
        continue;
      }
      if (this.state === "data" && control === ACK) {
        if (this.offset >= this.data.length) {
          this.state = "eotNak";
          this.currentPacket = new Uint8Array(0);
          void this.write(new Uint8Array([EOT]));
        } else {
          void this.sendNextDataBlock();
        }
        continue;
      }
      if (this.state === "data" && control === NAK && this.currentPacket.length) {
        void this.write(this.currentPacket);
        continue;
      }
      if (this.state === "eotNak" && control === NAK) {
        this.state = "eotAck";
        void this.write(new Uint8Array([EOT]));
        continue;
      }
      if (this.state === "eotNak" && control === ACK) {
        this.state = "endHeader";
        continue;
      }
      if (this.state === "eotAck" && control === ACK) {
        this.state = "endHeader";
        continue;
      }
      if (this.state === "endHeader" && control === CRC) {
        this.currentPacket = packet(SOH, 0, new Uint8Array(128));
        this.state = "done";
        void this.write(this.currentPacket);
        continue;
      }
      if (this.state === "done" && control === ACK) {
        this.finish();
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      if (control !== CRC && control !== NAK && control !== ACK) this.showVisibleByte(control);
    }
    this.flushVisible();
  }

  private async sendNextDataBlock() {
    if (this.sending || this.stopped) return;
    this.sending = true;
    if (this.offset >= this.data.length) {
      this.state = "eotNak";
      this.currentPacket = new Uint8Array(0);
      await this.write(new Uint8Array([EOT]));
      this.sending = false;
      return;
    }
    const { payload, sent } = paddedPayload(this.data, this.offset, 1024);
    this.currentPacket = packet(STX, this.sequence, payload);
    this.offset += sent;
    await this.write(this.currentPacket);
    this.sequence = (this.sequence + 1) & 0xff;
    this.progress(Math.min(this.offset, this.data.length), this.data.length);
    this.sending = false;
  }
}

export class YModemReceiver extends TransferBase {
  private state: "header" | "data" | "eot" | "endHeader" = "header";
  private expected = 1;
  private chunks: Uint8Array[] = [];
  private received = 0;
  private total = 0;
  private name = "ymodem-download.bin";

  constructor(write: BinaryWriter, progress: TransferProgress, done: (result?: TransferResult, error?: Error) => void, onVisible?: TransferVisible) {
    super(write, progress, done, onVisible);
    void write(new Uint8Array([CRC]));
  }

  push(data: Uint8Array) {
    this.append(data);
    while (!this.stopped && this.input.length) {
      if (this.input[0] === CAN) {
        this.input = this.input.slice(1);
        this.finish(undefined, new Error("远端取消传输"));
        this.showVisible(this.input);
        this.input = new Uint8Array(0);
        this.flushVisible();
        return;
      }
      if ((this.state === "data" || this.state === "eot") && this.input[0] === EOT) {
        this.input = this.input.slice(1);
        if (this.state === "data") {
          this.state = "eot";
          void this.write(new Uint8Array([NAK]));
        } else {
          this.state = "endHeader";
          void this.write(new Uint8Array([ACK, CRC]));
        }
        continue;
      }

      const parsed = parseIncomingPacket(this.input);
      if (parsed.wait) {
        this.flushVisible();
        return;
      }
      if (!parsed.frame || !parsed.size) {
        if (!isProtocolControl(this.input[0])) this.showVisibleByte(this.input[0]);
        this.input = this.input.slice(1);
        continue;
      }
      this.input = this.input.slice(parsed.frame.length);
      if (!isValidPacket(parsed.frame, parsed.size)) {
        void this.write(new Uint8Array([NAK]));
        continue;
      }

      if (this.state === "header" || this.state === "endHeader") {
        this.handleHeader(parsed.frame, parsed.size);
        continue;
      }
      this.handleData(parsed.frame, parsed.size);
    }
    this.flushVisible();
  }

  private handleHeader(frame: Uint8Array, size: number) {
    if (frame[1] !== 0) {
      void this.write(new Uint8Array([NAK]));
      return;
    }
    const payload = frame.slice(3, 3 + size);
    const file = readNullTerminated(payload);
    if (!file.value) {
      void this.write(new Uint8Array([ACK]));
      const data = concatMany(this.chunks).slice(0, this.total || this.received);
      this.finish({ name: this.name, data });
      this.showVisible(this.input);
      this.input = new Uint8Array(0);
      this.flushVisible();
      return;
    }
    const fileSize = readNullTerminated(payload, file.next);
    this.name = safeName(file.value);
    this.total = Number.parseInt(fileSize.value, 10) || 0;
    this.received = 0;
    this.expected = 1;
    this.chunks = [];
    this.state = "data";
    this.progress(0, this.total);
    void this.write(new Uint8Array([ACK, CRC]));
  }

  private handleData(frame: Uint8Array, size: number) {
    const sequence = frame[1];
    if (sequence === this.expected) {
      const payload = frame.slice(3, 3 + size);
      const remaining = this.total ? Math.max(this.total - this.received, 0) : payload.length;
      const useful = payload.slice(0, Math.min(payload.length, remaining || payload.length));
      this.chunks.push(useful);
      this.received += useful.length;
      this.expected = (this.expected + 1) & 0xff;
      this.progress(this.received, this.total);
    } else if (sequence !== previousSequence(this.expected)) {
      void this.write(new Uint8Array([NAK]));
      return;
    }
    void this.write(new Uint8Array([ACK]));
  }
}
