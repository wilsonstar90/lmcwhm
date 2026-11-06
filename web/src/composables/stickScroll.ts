// Log boxes follow new lines only while the reader is at the bottom: after
// scrolling up to read, a refresh leaves the view where it is.

/** True when el is scrolled to (or within a few lines of) its bottom. */
export function atBottom(el: HTMLElement | null | undefined, slack = 40): boolean {
  return !el || el.scrollHeight - el.scrollTop - el.clientHeight <= slack
}

/** Scrolls el to its bottom when `stick` (measured before the update) holds. */
export function follow(el: HTMLElement | null | undefined, stick: boolean) {
  if (el && stick) el.scrollTop = el.scrollHeight
}
