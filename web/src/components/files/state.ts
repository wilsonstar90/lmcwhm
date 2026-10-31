// State shared by every file manager tab: the clipboard, the editor window
// and the upload dialog.
import { reactive, ref } from 'vue'

export const clipboard = ref<{ mode: 'copy' | 'cut'; paths: string[] } | null>(null)

// The editor window opens files in tabs; the browser asks it to.
export const editorBus = reactive<{ open: string[]; seq: number }>({ open: [], seq: 0 })
export function openInEditor(path: string) {
  editorBus.open.push(path)
  editorBus.seq++
}

// The upload dialog: a target directory and, when dropped, the items.
export interface QueuedFile {
  file: File
  rel: string // path below the target directory
}
export const uploadBus = ref<{ dir: string; files: QueuedFile[]; seq: number } | null>(null)
export function openUpload(dir: string, files: QueuedFile[] = []) {
  uploadBus.value = { dir, files, seq: (uploadBus.value?.seq ?? 0) + 1 }
}

// Called by the upload dialog when it finished, so open tabs reload.
export const changed = ref(0)

export const join = (dir: string, name: string) => (dir === '/' ? '' : dir) + '/' + name
export const parentOf = (p: string) => p.replace(/\/[^/]+\/?$/, '') || '/'
export const baseName = (p: string) => p.split('/').pop() || p

const imageExt = /\.(png|jpe?g|gif|webp|bmp|ico|avif)$/i
export const isImage = (n: string) => imageExt.test(n)
export const isArchive = (n: string) => /\.(zip|tar\.gz|tgz|tar|gz)$/i.test(n)
const docExt = /\.(pdf|docx?|xlsx?|pptx?|odt|ods|txt|md|csv|rtf)$/i
export const isDoc = (n: string) => docExt.test(n)

// Files from a drop, folders walked (webkitGetAsEntry: Chrome, Edge, Safari).
export async function droppedFiles(dt: DataTransfer): Promise<QueuedFile[]> {
  const entries = Array.from(dt.items ?? [])
    .map((i) => (i.kind === 'file' ? i.webkitGetAsEntry?.() : null))
    .filter((e): e is FileSystemEntry => !!e)
  if (!entries.length) return Array.from(dt.files).map((file) => ({ file, rel: file.name }))
  const out: QueuedFile[] = []
  const walk = async (e: FileSystemEntry, prefix: string): Promise<void> => {
    if (e.isFile) {
      const file = await new Promise<File>((res, rej) => (e as FileSystemFileEntry).file(res, rej))
      out.push({ file, rel: prefix + e.name })
      return
    }
    const reader = (e as FileSystemDirectoryEntry).createReader()
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej))
      if (!batch.length) break
      for (const c of batch) await walk(c, prefix + e.name + '/')
    }
  }
  for (const e of entries) await walk(e, '')
  return out
}

// Icon for an entry (emoji keeps it light and works everywhere).
export function iconOf(name: string, dir: boolean): string {
  if (dir) return '📁'
  if (isImage(name) || /\.svg$/i.test(name)) return '🖼️'
  if (isArchive(name) || /\.(rar|7z|bz2|xz)$/i.test(name)) return '🗜️'
  if (/\.(php|js|ts|py|go|java|rb|sh|vue|css|scss|html?|json|ya?ml|xml|sql|conf|ini|env)$/i.test(name)) return '📝'
  if (/\.(mp4|webm|mov|mkv|avi|mp3|wav|flac|ogg)$/i.test(name)) return '🎞️'
  if (isDoc(name)) return '📄'
  return '📄'
}
