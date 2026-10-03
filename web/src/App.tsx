import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useAuth } from './auth'
import { Layout } from './components/Layout'
import { AnomaliesPage } from './pages/Anomalies'
import { ClientDetailPage } from './pages/ClientDetail'
import { ClientsPage } from './pages/Clients'
import { DomainDetailPage } from './pages/DomainDetail'
import { DomainsPage } from './pages/Domains'
import { LivePage } from './pages/Live'
import { LoginPage } from './pages/Login'
import { NotFoundPage } from './pages/NotFound'
import { OverviewPage } from './pages/Overview'
import { PipelinePage } from './pages/Pipeline'
import { SearchPage } from './pages/Search'
import { SettingsPage } from './pages/Settings'
import { SourcesPage } from './pages/Sources'
import { SourceDetailPage } from './pages/SourceDetail'

function RequireAuthentication() {
  const auth = useAuth()
  const location = useLocation()
  if (auth.loading) return <div className="auth-loading"><i /><span>Pulse</span></div>
  if (!auth.session || auth.session.state !== 'authenticated') return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return <Layout />
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="login" element={<LoginPage />} />
        <Route element={<RequireAuthentication />}>
          <Route index element={<OverviewPage />} />
          <Route path="overview" element={<OverviewPage />} />
          <Route path="live" element={<LivePage />} />
          <Route path="clients" element={<ClientsPage />} />
          <Route path="clients/:clientIP" element={<ClientDetailPage />} />
          <Route path="domains" element={<DomainsPage />} />
          <Route path="domains/:domain" element={<DomainDetailPage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="anomalies" element={<AnomaliesPage />} />
          <Route path="sources" element={<SourcesPage />} />
          <Route path="sources/:source" element={<SourceDetailPage />} />
          <Route path="pipeline" element={<PipelinePage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
