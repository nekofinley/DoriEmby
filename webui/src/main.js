import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router.js'
import { reveal } from './directives/reveal.js'
import { loadSite } from './site/store.js'
import './styles.css'
import './site/editor.css'

// The site's own copy is read before the first paint: the pages render from it,
// so mounting first would flash the built-in wording and then swap it out. A
// slow or missing endpoint must not hold the app hostage, hence the deadline.
const deadline = new Promise((resolve) => setTimeout(resolve, 2500))

Promise.race([loadSite().catch(() => {}), deadline]).then(() => {
  createApp(App).use(router).directive('reveal', reveal).mount('#app')
})
