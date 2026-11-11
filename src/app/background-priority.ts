// Background ownership only; never rewrite administrator-supplied HTML/CSS.
let configuredBackground = ""
const updateEvent = "openlist:background-config"

export const setConfiguredBackground = (value: string) => {
  configuredBackground = value
  window.dispatchEvent(new Event(updateEvent))
}

export const refreshBackgroundPriority = () =>
  window.dispatchEvent(new Event(updateEvent))

export const mountBackgroundPriority = (layer: HTMLDivElement) => {
  type Saved = { element: HTMLElement; value: string; priority: string }
  let saved: Saved[] = []
  let requestedUrl = ""
  let generation = 0
  let disposed = false
  const root = document.documentElement
  const set = (element: HTMLElement, property: string, value: string) => {
    if (
      element.style.getPropertyValue(property) !== value ||
      element.style.getPropertyPriority(property) !== "important"
    ) {
      element.style.setProperty(property, value, "important")
    }
  }
  const release = () => {
    for (const { element, value, priority } of saved) {
      // Don't erase a later inline change made by user code.
      if (
        element.style.getPropertyValue("background-image") === "none" &&
        element.style.getPropertyPriority("background-image") === "important"
      ) {
        if (value)
          element.style.setProperty("background-image", value, priority)
        else element.style.removeProperty("background-image")
      }
    }
    saved = []
  }
  const loadImage = (url: string) => {
    requestedUrl = url
    const revision = ++generation
    const image = new Image()
    image.className = "home-background-image"
    image.alt = ""
    image.decoding = "async"
    // Send only the real site origin to cross-origin image allowlists. The
    // app's global same-origin referrer policy otherwise omits it entirely.
    image.referrerPolicy = "strict-origin-when-cross-origin"
    image.loading = "eager"
    image.fetchPriority = "low"
    image.draggable = false
    layer.dataset.backgroundState = "loading"
    const failed = () => {
      if (disposed || revision !== generation) return
      // Keep the last decoded wallpaper if its replacement cannot be loaded.
      layer.dataset.backgroundState = "error"
    }
    image.onload = async () => {
      try {
        await image.decode()
      } catch {
        failed()
        return
      }
      if (disposed || revision !== generation) return
      // Decode offscreen, then swap atomically: neither progressive loading
      // nor a URL change should expose an empty or partially decoded image.
      layer.replaceChildren(image)
      layer.dataset.backgroundState = "ready"
    }
    image.onerror = failed
    image.src = url
  }
  const update = () => {
    const active =
      root.classList.contains("openlist-glass-home") && !!configuredBackground
    if (active) {
      if (!saved.length)
        saved = [root, document.body].map((element) => ({
          element,
          value: element.style.getPropertyValue("background-image"),
          priority: element.style.getPropertyPriority("background-image"),
        }))
      // Suppress custom images only while the configured wallpaper owns them.
      for (const { element } of saved) set(element, "background-image", "none")
      set(layer, "background-image", "none")
      set(layer, "display", "block")
      if (
        window.__openlistPrimaryReady &&
        requestedUrl !== configuredBackground
      )
        loadImage(configuredBackground)
    } else {
      release()
      set(layer, "display", "none")
      if (!configuredBackground) {
        ++generation
        requestedUrl = ""
        layer.replaceChildren()
        delete layer.dataset.backgroundState
      }
      // Keep the image node across route changes; no scroll-triggered reloads.
    }
    layer.dataset.backgroundSource = active ? "settings" : "custom"
  }
  window.addEventListener(updateEvent, update)
  window.addEventListener("openlist:primary-ready", update, { once: true })
  update()
  return () => {
    disposed = true
    ++generation
    window.removeEventListener(updateEvent, update)
    window.removeEventListener("openlist:primary-ready", update)
    release()
    layer.replaceChildren()
    layer.style.removeProperty("background-image")
    layer.style.removeProperty("display")
    delete layer.dataset.backgroundState
    delete layer.dataset.backgroundSource
  }
}
