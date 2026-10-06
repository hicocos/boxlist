export function splitFileName(name: string, directory = false) {
  const dot = name.lastIndexOf(".")
  const hasExtension = !directory && dot > 0 && dot < name.length - 1
  return {
    stem: hasExtension ? name.slice(0, dot) : name,
    extension: hasExtension ? name.slice(dot) : "",
  }
}

/** A fixed trailing fragment plus a flexing prefix gives CSS middle ellipsis. */
export function filenameParts(name: string, directory = false) {
  const { stem, extension } = splitFileName(name, directory)
  const points = Array.from(stem)
  const tailLength = points.length > 20 ? 8 : 0
  return {
    start: tailLength ? points.slice(0, -tailLength).join("") : stem,
    end: (tailLength ? points.slice(-tailLength).join("") : "") + extension,
  }
}

export function changedNameParts(before: string, after: string) {
  const a = Array.from(before),
    b = Array.from(after)
  let start = 0,
    end = 0
  while (start < a.length && start < b.length && a[start] === b[start]) start++
  while (
    end < a.length - start &&
    end < b.length - start &&
    a[a.length - end - 1] === b[b.length - end - 1]
  )
    end++
  return {
    prefix: a.slice(0, start).join(""),
    removed: a.slice(start, a.length - end).join(""),
    added: b.slice(start, b.length - end).join(""),
    suffix: end ? a.slice(-end).join("") : "",
  }
}
