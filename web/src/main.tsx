import '@fontsource-variable/inter'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { AuthProvider } from './auth'
import { LanguageProvider } from './i18n'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <LanguageProvider><AuthProvider><App /></AuthProvider></LanguageProvider>
  </StrictMode>,
)
