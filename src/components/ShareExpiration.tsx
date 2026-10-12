import { Button, FormControl, FormLabel, HStack, Input, Text } from "@hope-ui/solid"
import { Accessor, createSignal, createUniqueId, For, Show } from "solid-js"
import { useT } from "~/hooks"

export interface ShareExpirationState {
  option: Accessor<string>
  custom: Accessor<string>
  error: Accessor<boolean>
  reset(value?: string | null): void
  select(value: string): void
  updateCustom(value: string): void
  resolve(): string | null | undefined
}

const durations: Record<string, number> = {
  hour: 3600000,
  day: 86400000,
  week: 7 * 86400000,
  month: 30 * 86400000,
}

export function createShareExpiration(): ShareExpirationState {
  const [option, setOption] = createSignal("never")
  const [custom, setCustom] = createSignal("")
  const [error, setError] = createSignal(false)
  let original: string | null = null
  let changed = false
  return {
    option, custom, error,
    reset(value: string | null = null) {
      original = value
      changed = false
      setError(false)
      setOption(value ? "custom" : "never")
      if (value) {
        const date = new Date(value)
        setCustom(new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19))
      } else {
        setCustom("")
      }
    },
    select(value: string) {
      changed = true
      setOption(value)
      setError(value === "custom" && (!custom() || !(new Date(custom()).getTime() > Date.now())))
    },
    updateCustom(value: string) {
      changed = true
      setCustom(value)
      setError(!value || !(new Date(value).getTime() > Date.now()))
    },
    resolve(): string | null | undefined {
      if (!changed) return original
      if (option() === "never") return null
      if (option() === "custom") {
        const date = new Date(custom())
        if (!custom() || !(date.getTime() > Date.now())) {
          setError(true)
          return undefined
        }
        return date.toISOString()
      }
      return new Date(Date.now() + durations[option()]).toISOString()
    },
  }
}

export function ShareExpiration(props: {
  action: "create" | "update" | "confirm"
  state: ShareExpirationState
}) {
  const t = useT()
  const id = createUniqueId()
  return (
    <FormControl w="$full" minW="0">
      <FormLabel id={`${id}-label`}>{t("shares.expires")}</FormLabel>
      <HStack role="group" aria-labelledby={`${id}-label`} spacing="$2" gap="$2" wrap="wrap">
        <For each={["never", "hour", "day", "week", "month", "custom"]}>
          {(option) => (
            <Button
              type="button"
              size="sm"
              variant={props.state.option() === option ? "solid" : "subtle"}
              aria-pressed={props.state.option() === option}
              onClick={() => props.state.select(option)}
            >
              {t(`shares.expiration.${option}`)}
            </Button>
          )}
        </For>
      </HStack>
      <Show when={props.state.option() === "custom"}>
        <Input
          mt="$2"
          minW="0"
          aria-label={t("shares.expiration.custom")}
          aria-describedby={`${id}-help`}
          type="datetime-local"
          step={1}
          value={props.state.custom()}
          invalid={props.state.error()}
          onInput={(event) => props.state.updateCustom(event.currentTarget.value)}
        />
      </Show>
      <Text id={`${id}-help`} mt="$2" fontSize="$sm" color="$neutral11">
        {props.state.option() === "custom"
          ? `${t("shares.expiration.timezone")} (${Intl.DateTimeFormat().resolvedOptions().timeZone})`
          : props.state.option() === "never"
            ? t("shares.expiration.never_hint")
            : t(`shares.expiration.${props.action}_hint`, {
                duration: t(`shares.expiration.${props.state.option()}`),
              })}
      </Text>
      <Show when={props.state.error()}>
        <Text role="alert" mt="$1" fontSize="$sm" color="$danger11">
          {t("shares.expiration.future_required")}
        </Text>
      </Show>
    </FormControl>
  )
}
