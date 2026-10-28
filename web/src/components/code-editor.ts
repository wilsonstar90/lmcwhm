import { defineAsyncComponent } from 'vue'

// The code editor (CodeMirror, ~200 KB gzipped) loads the first time one is shown.
export const CodeEditor = defineAsyncComponent(() => import('./CodeEditor.vue'))
