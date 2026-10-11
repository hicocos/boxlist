(() => {
  const activate = () => {
    const template = document.getElementById("deferred-customize-body")
    if (!template) return
    const fragment = template.content
    // Template contents are inert: neither scripts nor images load before here.
    const images = [...fragment.querySelectorAll("img[src]")]
    for (const image of images) {
      image.dataset.deferredSrc = image.getAttribute("src")
      image.removeAttribute("src")
      image.loading = "lazy"
      image.decoding = "async"
      image.fetchPriority = "low"
    }
    const scripts = [...fragment.querySelectorAll("script")]
    for (const script of scripts) script.remove()
    document.body.append(fragment)
    template.remove()
    const observer = new IntersectionObserver(entries => {
      for (const { target, isIntersecting } of entries) {
        if (!isIntersecting) continue
        target.src = target.dataset.deferredSrc
        delete target.dataset.deferredSrc
        observer.unobserve(target)
      }
    }, { rootMargin: "200px" })
    for (const image of images) observer.observe(image)
    // Execute administrator scripts only after their DOM exists, preserving order.
    void (async () => {
      for (const original of scripts) {
        const script = document.createElement("script")
        for (const attr of original.attributes) script.setAttribute(attr.name, attr.value)
        script.textContent = original.textContent
        if (script.src) {
          script.async = false
          await new Promise(resolve => {
            script.onload = script.onerror = resolve
            document.body.append(script)
          })
        } else document.body.append(script)
      }
    })()
  }
  const schedule = () => {
    if ("requestIdleCallback" in window) window.requestIdleCallback(activate, { timeout: 1500 })
    else setTimeout(activate, 0)
  }
  if (window.__openlistPrimaryReady) schedule()
  else window.addEventListener("openlist:primary-ready", schedule, { once: true })
})()
