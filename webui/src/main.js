import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router.js'
import { reveal } from './directives/reveal.js'
import './styles.css'

createApp(App).use(router).directive('reveal', reveal).mount('#app')
