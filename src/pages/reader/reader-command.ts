export interface ReaderCommand {
  index: number
  fraction: number
}

/** Always return a fresh command, including repeated jumps to the same page. */
export function readerCommand(index: number, fraction = 0): ReaderCommand {
  return {
    index: Number.isFinite(index) ? Math.max(0, Math.trunc(index)) : 0,
    fraction: Number.isFinite(fraction)
      ? Math.max(0, Math.min(1, fraction))
      : 0,
  }
}

export function isPlainReaderClick(event: {
  button: number
  defaultPrevented: boolean
  altKey: boolean
  ctrlKey: boolean
  metaKey: boolean
  shiftKey: boolean
}) {
  return (
    !event.defaultPrevented &&
    event.button === 0 &&
    !event.altKey &&
    !event.ctrlKey &&
    !event.metaKey &&
    !event.shiftKey
  )
}
