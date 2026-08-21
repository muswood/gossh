// owner: muswood | Email: mumu920@outlook.com
import { mount } from 'svelte'
import App from './App.svelte'
import './style.css'
import '@xterm/xterm/css/xterm.css'

const target = document.getElementById('app')
const app = mount(App, { target: target || document.body })

export default app
