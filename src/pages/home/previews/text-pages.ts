// Bound layout work even for files containing a single extremely long line.
const pageCharacters = 12000

export function textPageOffsets(text: string): number[] {
  const offsets = [0]
  let start = 0
  while (start < text.length) {
    let end = Math.min(start + pageCharacters, text.length)
    if (end < text.length) {
      const newline = text.lastIndexOf("\n", end - 1)
      if (newline >= start + pageCharacters / 2) end = newline + 1
      // Do not split UTF-16 surrogate pairs or CRLF line endings.
      const last = text.charCodeAt(end - 1)
      if ((last >= 0xd800 && last <= 0xdbff) ||
          (text[end - 1] === "\r" && text[end] === "\n")) end--
    }
    offsets.push(end)
    start = end
  }
  if (offsets.length === 1) offsets.push(0)
  return offsets
}
