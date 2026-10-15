import "./file-rows.css"

export const FileName = (props: {
  name: string
  mode?: string
}) => {
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
      {props.name}
    </span>
  )
}
