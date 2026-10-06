import axios from "axios"
import {
  batch,
  createEffect,
  createMemo,
  createSignal,
  lazy,
  on,
  onCleanup,
  Show,
  Suspense,
} from "solid-js"
import { useRouter } from "~/hooks"
import { getLinkByDirAndObj } from "~/hooks/useLink"
import { me, password } from "~/store"
import { Obj } from "~/types"
import { fsList, joinBase } from "~/utils"
import { getDirectoryReturnHref } from "~/store/history"
import { isPlainReaderClick, readerCommand } from "./reader-command"
import {
  isReaderImage,
  readProgress,
  writeProgress,
  validDirectory,
  ReaderImage,
} from "./reader-utils"
import SingleReader from "./SingleReader"
import "./reader.css"

const ContinuousReader = lazy(() => import("./ContinuousReader"))

export default function Reader() {
  const { searchParams, setSearchParams, to } = useRouter()
  const directory = createMemo(() =>
    typeof searchParams.dir === "string" ? searchParams.dir : "/",
  )
  const [files, setFiles] = createSignal<Obj[]>([])
  const [loading, setLoading] = createSignal(true)
  const [loadedEntries, setLoadedEntries] = createSignal(0)
  const [totalEntries, setTotalEntries] = createSignal(0)
  const [error, setError] = createSignal("")
  const [reload, setReload] = createSignal(0)
  const [mode, setMode] = createSignal<"continuous" | "single">("continuous")

  const [index, setIndex] = createSignal(0)
  const [fraction, setFraction] = createSignal(0)
  const [command, setCommand] = createSignal(readerCommand(0))
  const [jumpValue, setJumpValue] = createSignal("1")
  const [notice, setNotice] = createSignal("")
  const [fullscreen, setFullscreen] = createSignal(false)
  const collator = new Intl.Collator("zh-CN", {
    numeric: true,
    sensitivity: "base",
  })
  const key = () => `${me().id}:${me().base_path}:${directory()}`
  const sorted = createMemo(() =>
    [...files()].sort(
      (a, b) =>
        collator.compare(a.name, b.name) || a.name.localeCompare(b.name),
    ),
  )
  const items = createMemo<ReaderImage[]>(() =>
    sorted().map((o) => ({
      key: o.name,
      name: o.name,
      size: o.size,
      src: getLinkByDirAndObj(directory(), o, "direct", false, true),
    })),
  )
  let ready = false
  let loadedProgressKey = ""
  let saveTimer: ReturnType<typeof setTimeout> | undefined
  const save = () => {
    if (!ready || !items()[index()]) return
    writeProgress(loadedProgressKey, {
      name: items()[index()].name,
      fraction: fraction(),
      mode: mode(),
      reverse: false,
      gap: 0,
      time: Date.now(),
    })
  }
  const saveSoon = () => {
    clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      save()
      const current = items()[index()]
      if (
        ready &&
        current &&
        (searchParams.file !== current.name || searchParams.mode !== mode())
      )
        setSearchParams({ file: current.name, mode: mode() }, { replace: true })
    }, 500)
  }
  const progress = (next: number, part: number) => {
    if (!ready || next < 0 || next >= items().length) return
    batch(() => {
      setIndex(next)
      setFraction(Math.max(0, Math.min(1, part)))
      setJumpValue(String(next + 1))
    })
    saveSoon()
  }
  const jump = (next: number, part = 0) => {
    if (!items().length) return
    next = Math.max(0, Math.min(items().length - 1, next))
    batch(() => {
      setCommand(readerCommand(next, part))
      progress(next, part)
    })
    setSearchParams(
      { file: items()[next].name, mode: mode() },
      { replace: true },
    )
  }
  const changeMode = (value: "continuous" | "single") => {
    batch(() => {
      setCommand(readerCommand(index(), fraction()))
      setMode(value)
    })
    setSearchParams(
      { mode: value, file: items()[index()]?.name },
      { replace: true },
    )
    saveSoon()
  }

  createEffect(
    on([directory, reload], async () => {
      save()
      ready = false
      const dir = directory()
      const cancel = axios.CancelToken.source()
      let stopped = false
      onCleanup(() => {
        stopped = true
        cancel.cancel()
      })
      batch(() => {
        setLoading(true)
        setError("")
        setFiles([])
        setLoadedEntries(0)
        setTotalEntries(0)
        setNotice("")
      })
      if (!validDirectory(dir)) {
        setError("目录地址无效，请从文件目录进入阅读页。")
        setLoading(false)
        return
      }
      const resume = readProgress(key())
      const explicitFile =
        typeof searchParams.file === "string" ? searchParams.file : ""
      const initialMode =
        searchParams.mode === "single"
          ? "single"
          : searchParams.mode === "continuous"
            ? "continuous"
            : resume?.mode || "continuous"
      // Old order/gap preferences are intentionally retired; keep only position.
      setMode(initialMode)
      const collected = new Map<string, Obj>()
      const seen = new Set<string>()
      let count = 0
      try {
        for (let page = 1; ; page++) {
          const timeout = setTimeout(
            () => cancel.cancel("目录请求超时，请重试"),
            30000,
          )
          const response = await fsList(
            dir,
            password(),
            page,
            200,
            false,
            cancel.token,
          )
          clearTimeout(timeout)
          if (stopped) return
          if (response.code !== 200)
            throw new Error(response.message || "目录读取失败")
          const content = response.data.content || []
          const total = response.data.total
          if (!Number.isFinite(total) || total < 0)
            throw new Error("目录返回了无效的总数")
          let added = 0
          for (const obj of content) {
            if (seen.has(obj.name)) continue
            seen.add(obj.name)
            added++
            if (isReaderImage(obj)) collected.set(obj.name, obj)
          }
          count += added
          setLoadedEntries(count)
          setTotalEntries(total)
          if (count > 100000) throw new Error("目录过大，请按章节拆分后阅读")
          if (count >= total) break
          if (!added || !content.length)
            throw new Error("目录分页未返回完整内容，请重试")
          if (page >= 500 || count >= 100000)
            throw new Error(
              "目录过大，请按章节拆分后阅读（本页最多读取十万条目录记录）",
            )
        }
        if (stopped) return
        setFiles([...collected.values()])
        let next = Math.max(
          0,
          items().findIndex((o) => o.name === (explicitFile || resume?.name)),
        )
        let part =
          resume && resume.name === items()[next]?.name ? resume.fraction : 0
        batch(() => {
          setIndex(next)
          setFraction(part)
          setCommand(readerCommand(next, part))
          setJumpValue(String(next + 1))
        })

        loadedProgressKey = key()
        ready = true
        setLoading(false)
      } catch (e) {
        if (stopped) return
        setError(e instanceof Error ? e.message : "目录读取失败")
        setLoading(false)
      }
    }),
  )
  const fullChange = () => setFullscreen(!!document.fullscreenElement)
  document.addEventListener("fullscreenchange", fullChange)
  window.addEventListener("pagehide", save)
  const hidden = () => {
    if (document.hidden) save()
  }
  document.addEventListener("visibilitychange", hidden)
  onCleanup(() => {
    save()
    clearTimeout(saveTimer)
    document.removeEventListener("fullscreenchange", fullChange)
    document.removeEventListener("visibilitychange", hidden)
    window.removeEventListener("pagehide", save)
  })
  const toggleFullscreen = async () => {
    try {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await document.documentElement.requestFullscreen()
    } catch {
      setNotice("当前浏览器不支持网页全屏，可以使用浏览器的全屏功能。")
    }
  }
  const returnUrl = () =>
    joinBase(
      getDirectoryReturnHref(validDirectory(directory()) ? directory() : "/"),
    )
  const returnToDirectory = (event: MouseEvent) => {
    save()
    if (!isPlainReaderClick(event)) return
    event.preventDefault()
    to(returnUrl(), true, { preserveHistory: true, scroll: false })
  }
  return (
    <main class="directory-reader">
      <div class="reader-chrome">
        <header class="reader-header">
          <a
            class="reader-return"
            href={returnUrl()}
            data-restore-history
            onClick={returnToDirectory}
          >
            ‹ 返回目录
          </a>
          <strong class="reader-title" title={directory()}>
            {directory().split("/").filter(Boolean).pop() || "首页"}
          </strong>
          <button
            type="button"
            onClick={toggleFullscreen}
            aria-label={fullscreen() ? "退出全屏" : "全屏"}
          >
            {fullscreen() ? "退出全屏" : "全屏"}
          </button>
          <a
            href={returnUrl()}
            data-restore-history
            onClick={returnToDirectory}
            aria-label="关闭阅读页"
            class="reader-close"
          >
            ×
          </a>
        </header>
        <Show when={!loading() && !error() && items().length > 0}>
          <nav class="reader-controls" aria-label="阅读设置">
            <div class="reader-mode">
              <button
                type="button"
                aria-pressed={mode() === "continuous"}
                onClick={() => changeMode("continuous")}
              >
                连读
              </button>
              <button
                type="button"
                aria-pressed={mode() === "single"}
                onClick={() => changeMode("single")}
              >
                单张
              </button>
            </div>

            <form
              class="reader-jump"
              onSubmit={(e) => {
                e.preventDefault()
                const n = Number(jumpValue())
                if (Number.isInteger(n)) jump(n - 1)
              }}
            >
              <input
                type="number"
                min="1"
                max={items().length}
                aria-label="跳到第几张"
                value={jumpValue()}
                onInput={(e) => setJumpValue(e.currentTarget.value)}
              />
              <span>/ {items().length}</span>
              <button type="submit">跳转</button>
            </form>
          </nav>
        </Show>
      </div>
      <Show when={notice()}>
        <p class="reader-notice" role="status">
          {notice()}
          <button
            type="button"
            aria-label="关闭提示"
            onClick={() => setNotice("")}
          >
            ×
          </button>
        </p>
      </Show>
      <Show when={loading()}>
        <p class="reader-message" role="status">
          正在读取目录… {loadedEntries()} / {totalEntries() || "…"} 条<br />
          只读取文件信息，不会同时下载所有原图。
        </p>
      </Show>
      <Show when={error()}>
        <div class="reader-message" role="alert">
          <p>{error()}</p>
          <p>加密或受限目录请先返回目录解锁，阅读页不绕过访问权限。</p>
          <button type="button" onClick={() => setReload(reload() + 1)}>
            重试
          </button>
          <a
            href={returnUrl()}
            data-restore-history
            onClick={returnToDirectory}
          >
            返回目录
          </a>
        </div>
      </Show>
      <Show when={!loading() && !error()}>
        <Show
          when={items().length > 0}
          fallback={
            <p class="reader-message">
              此目录没有可直接显示的图片。支持 JPG、PNG、GIF、WebP、SVG、AVIF
              等浏览器原生格式。
            </p>
          }
        >
          <Show
            when={mode() === "continuous"}
            fallback={
              <SingleReader
                items={items()}
                index={index()}
                onChange={(i) => jump(i)}
              />
            }
          >
            <Suspense
              fallback={<p class="reader-message">正在打开连读模式…</p>}
            >
              <ContinuousReader
                items={items()}
                command={command()}
                gap={0}
                onProgress={progress}
                onOpen={(i: number) => {
                  jump(i)
                  changeMode("single")
                }}
              />
            </Suspense>
          </Show>
        </Show>
      </Show>
    </main>
  )
}
