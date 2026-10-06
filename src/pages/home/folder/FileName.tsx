import { createMemo, Show } from "solid-js"
import { filenameParts } from "~/utils/file-names"
import "./file-rows.css"

export const FileName = (props: {
  name: string
  directory?: boolean
  mode?: string
}) => {
  const parts = createMemo(() => filenameParts(props.name, props.directory))
  return (
    <span
      class="name ux-filename"
      classList={{
        "ux-filename-lines": props.mode === "multi_line",
        "ux-filename-scroll": props.mode === "scrollable",
      }}
      aria-label={props.name}
      title={props.name}
    >
      <Show
        when={!props.mode || props.mode === "ellipsis"}
        fallback={props.name}
      >
        <span class="ux-filename-start">{parts().start}</span>
        <span class="ux-filename-end">{parts().end}</span>
      </Show>
    </span>
  )
}
