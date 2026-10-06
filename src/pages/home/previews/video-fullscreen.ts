export const wantsAutoFullscreen = (value: unknown) => value === "true"

/** Property assignment discards async setter promises. Invoke the descriptor
 * explicitly so Artplayer's native fullscreen rejection is always handled. */
export async function setNativeFullscreen(
  player: object,
  enabled: boolean,
): Promise<boolean> {
  try {
    let target: object | null = player
    while (target) {
      const descriptor = Object.getOwnPropertyDescriptor(target, "fullscreen")
      if (descriptor?.set) {
        await descriptor.set.call(player, enabled)
        return Reflect.get(player, "fullscreen") === enabled
      }
      target = Object.getPrototypeOf(target)
    }
  } catch {
    // Native fullscreen may require a fresh user gesture. Keep the player inline.
  }
  return false
}
