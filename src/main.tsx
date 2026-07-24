import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'

// Apply the persisted theme before React paints. Login is intentionally always
// dark, while authenticated routes remember the user's Light/Dark choice.
const rootElement = document.documentElement
const isLoginRoute =
  window.location.hash.toLowerCase().startsWith('#/login') ||
  sessionStorage.getItem('isLoggedIn') !== 'true'
const storedTheme = localStorage.getItem('roobuck-theme')
const initialTheme = isLoginRoute ? 'dark' : storedTheme === 'light' ? 'light' : 'dark'
rootElement.classList.toggle('dark', initialTheme === 'dark')
rootElement.classList.toggle('light', initialTheme === 'light')

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
)
