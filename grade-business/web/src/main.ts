import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import './style.css'
import './v2.css'
import './regulator.css'

createApp(App).use(router).mount('#app')
