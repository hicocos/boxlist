import crypto from "crypto-js"

const format = "boxlist-gzip-aes"

async function gzip(blob: Blob, decompress = false): Promise<Blob> {
  const stream = decompress
    ? new DecompressionStream("gzip")
    : new CompressionStream("gzip")
  return new Response(blob.stream().pipeThrough(stream)).blob()
}

export async function encodeBackup(data: unknown, password: string) {
  const compressed = await gzip(new Blob([JSON.stringify(data)]))
  if (!password) return { blob: compressed, extension: ".json.gz" }
  const bytes = new Uint8Array(await compressed.arrayBuffer())
  const payload = crypto.AES.encrypt(
    crypto.lib.WordArray.create(bytes),
    password,
  ).toString()
  return {
    blob: new Blob([JSON.stringify({ format, version: 1, payload })], {
      type: "application/json",
    }),
    extension: ".json",
  }
}

function decryptLegacy(value: unknown, password: string) {
  if (typeof value !== "string") throw new Error("Invalid encrypted backup")
  const ciphertext = crypto.enc.Base64.parse(value).toString(crypto.enc.Utf8)
  return JSON.parse(crypto.AES.decrypt(ciphertext, password).toString(crypto.enc.Utf8))
}

export async function decodeBackup(file: Blob, password: string) {
  const header = new Uint8Array(await file.slice(0, 2).arrayBuffer())
  let data = JSON.parse(await (header[0] === 0x1f && header[1] === 0x8b
    ? await gzip(file, true)
    : file).text())
  if (data.format === format) {
    if (data.version !== 1 || typeof data.payload !== "string") {
      throw new Error("Unsupported backup format")
    }
    try {
      const words = crypto.AES.decrypt(data.payload, password)
      if (words.sigBytes <= 0) throw new Error()
      const bytes = new Uint8Array(words.sigBytes)
      for (let i = 0; i < bytes.length; i++) {
        bytes[i] = (words.words[i >>> 2] >>> (24 - (i % 4) * 8)) & 255
      }
      data = JSON.parse(await (await gzip(new Blob([bytes]), true)).text())
    } catch {
      throw new Error("Incorrect password or damaged backup")
    }
  }
  if (data.encrypted) {
    try {
      if (decryptLegacy(data.encrypted, password) !== "encrypted") throw new Error()
      for (const name of ["settings", "users", "storages", "metas", "shares"]) {
        for (const row of data[name] || []) {
          for (const key of Object.keys(row)) row[key] = decryptLegacy(row[key], password)
        }
      }
      if (data.derivatives !== undefined) {
        data.derivatives = decryptLegacy(data.derivatives, password)
      }
      data.encrypted = ""
    } catch {
      throw new Error("Incorrect password or damaged backup")
    }
  }
  return data
}
