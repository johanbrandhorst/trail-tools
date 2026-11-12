/**
 * A zip archive keeps a directory of its files at its end, saying where
 * each starts, so single files can be read from a large archive without
 * reading the rest of it: a few slices of the file, read from disk as
 * needed, rather than the whole archive in memory. Archives over 4 GB use
 * the zip64 records, which give the sizes and positions in 64 bits.
 */

// ZipEntry is a file of a zip archive, as its central directory lists it.
export interface ZipEntry {
  name: string;
  // 0 if stored as is, 8 if compressed with deflate.
  method: number;
  compressedSize: number;
  // Where the entry's local header starts.
  offset: number;
  encrypted: boolean;
}

const END_OF_CENTRAL_DIRECTORY = 0x06_05_4b_50;
const ZIP64_LOCATOR = 0x07_06_4b_50;
const ZIP64_END_OF_CENTRAL_DIRECTORY = 0x06_06_4b_50;
const CENTRAL_DIRECTORY_ENTRY = 0x02_01_4b_50;
const LOCAL_HEADER = 0x04_03_4b_50;
// The value of 32 bit fields whose value is in a zip64 record instead.
const ZIP64_MARKER = 0xff_ff_ff_ff;
// The end of central directory record, and its comment of at most 64 KB.
const MAX_END_SIZE = 22 + 0xff_ff;

const readView = async (
  file: Blob,
  start: number,
  end: number
): Promise<DataView> =>
  new DataView(await file.slice(start, end).arrayBuffer());

// Uint64 reads a little endian 64 bit number, exact up to 2^53.
const uint64 = (view: DataView, offset: number): number =>
  view.getUint32(offset, true) + view.getUint32(offset + 4, true) * 2 ** 32;

const notZip = (): Error =>
  new Error("This isn't a zip file. Choose the zip Strava emailed you.");

/**
 * ReadZipEntries reads the directory of a zip archive, its entries by name.
 */
export const readZipEntries = async (
  file: Blob
): Promise<Map<string, ZipEntry>> => {
  const tailStart = Math.max(0, file.size - MAX_END_SIZE);
  const tail = await readView(file, tailStart, file.size);
  let end = -1;
  for (let at = tail.byteLength - 22; at >= 0; at -= 1) {
    if (tail.getUint32(at, true) === END_OF_CENTRAL_DIRECTORY) {
      end = at;
      break;
    }
  }
  if (end === -1) {
    throw notZip();
  }
  let count = tail.getUint16(end + 10, true);
  let directorySize = tail.getUint32(end + 12, true);
  let directoryOffset = tail.getUint32(end + 16, true);
  if (
    count === 0xff_ff ||
    directorySize === ZIP64_MARKER ||
    directoryOffset === ZIP64_MARKER
  ) {
    const locator = end - 20;
    if (locator < 0 || tail.getUint32(locator, true) !== ZIP64_LOCATOR) {
      throw notZip();
    }
    const recordOffset = uint64(tail, locator + 8);
    const record = await readView(file, recordOffset, recordOffset + 56);
    if (record.getUint32(0, true) !== ZIP64_END_OF_CENTRAL_DIRECTORY) {
      throw notZip();
    }
    count = uint64(record, 32);
    directorySize = uint64(record, 40);
    directoryOffset = uint64(record, 48);
  }
  const directory = await readView(
    file,
    directoryOffset,
    directoryOffset + directorySize
  );
  const decoder = new TextDecoder();
  const entries = new Map<string, ZipEntry>();
  let position = 0;
  for (
    let index = 0;
    index < count && position + 46 <= directory.byteLength;
    index += 1
  ) {
    if (directory.getUint32(position, true) !== CENTRAL_DIRECTORY_ENTRY) {
      throw notZip();
    }
    const flags = directory.getUint16(position + 8, true);
    const method = directory.getUint16(position + 10, true);
    let compressedSize = directory.getUint32(position + 20, true);
    const uncompressedSize = directory.getUint32(position + 24, true);
    const nameLength = directory.getUint16(position + 28, true);
    const extraLength = directory.getUint16(position + 30, true);
    const commentLength = directory.getUint16(position + 32, true);
    let offset = directory.getUint32(position + 42, true);
    const name = decoder.decode(
      new Uint8Array(
        directory.buffer,
        directory.byteOffset + position + 46,
        nameLength
      )
    );
    /*
     * The zip64 extra field has the sizes and offset that don't fit in 32
     * bits, in order, and only those.
     */
    let extra = position + 46 + nameLength;
    const extraEnd = extra + extraLength;
    while (extra + 4 <= extraEnd) {
      const id = directory.getUint16(extra, true);
      const size = directory.getUint16(extra + 2, true);
      if (id === 1) {
        let field = extra + 4;
        if (uncompressedSize === ZIP64_MARKER) {
          field += 8;
        }
        if (compressedSize === ZIP64_MARKER) {
          compressedSize = uint64(directory, field);
          field += 8;
        }
        if (offset === ZIP64_MARKER) {
          offset = uint64(directory, field);
        }
      }
      extra += 4 + size;
    }
    entries.set(name, {
      compressedSize,
      encrypted: (flags & 1) === 1,
      method,
      name,
      offset,
    });
    position = extraEnd + commentLength;
  }
  return entries;
};

/**
 * ReadZipEntry reads a file of a zip archive, decompressing it with the
 * browser's own deflate.
 */
export const readZipEntry = async (
  file: Blob,
  entry: ZipEntry
): Promise<Uint8Array> => {
  if (entry.encrypted) {
    throw new Error(`${entry.name} is encrypted.`);
  }
  const header = await readView(file, entry.offset, entry.offset + 30);
  if (header.getUint32(0, true) !== LOCAL_HEADER) {
    throw notZip();
  }
  const start =
    entry.offset + 30 + header.getUint16(26, true) + header.getUint16(28, true);
  const data = file.slice(start, start + entry.compressedSize);
  switch (entry.method) {
    case 0: {
      return new Uint8Array(await data.arrayBuffer());
    }
    case 8: {
      const stream = data
        .stream()
        .pipeThrough(new DecompressionStream("deflate-raw"));
      return new Uint8Array(await new Response(stream).arrayBuffer());
    }
    default: {
      throw new Error(
        `${entry.name} is compressed in a way that can't be read.`
      );
    }
  }
};
