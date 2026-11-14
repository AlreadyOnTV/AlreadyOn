// Every profile uses the same Modern Minimal Dark palette and device-managed display sizing.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import './index.css'
import './theme/themes.css'
import { UsersProvider } from './users/UsersProvider'
import { GuideSettingsProvider } from './settings/GuideSettingsProvider'
import App from './App.tsx'
import { AuthGate } from './users/AuthGate'
import AutofillPolicy from './components/AutofillPolicy'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AutofillPolicy />
    <BrowserRouter>
      <AuthGate>
      <UsersProvider>
            <GuideSettingsProvider>
              <App />
            </GuideSettingsProvider>
      </UsersProvider>
      </AuthGate>
    </BrowserRouter>
  </StrictMode>,
)
