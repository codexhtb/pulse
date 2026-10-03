import {
  Activity,
  AlertTriangle,
  Database,
  Globe2,
  Gauge,
  LayoutDashboard,
  Menu,
  LogOut,
  Radio,
  Search,
  Server,
  Settings,
  Users,
  X,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { useI18n } from '../i18n'
import { useAuth } from '../auth'

const navigation = [
  { to: '/', label: 'Overview', icon: LayoutDashboard, end: true },
  { to: '/live', label: 'Live', icon: Radio },
  { to: '/clients', label: 'Clients', icon: Users },
  { to: '/domains', label: 'Domains', icon: Globe2 },
  { to: '/search', label: 'Search', icon: Search },
  { to: '/anomalies', label: 'Anomalies', icon: AlertTriangle },
  { to: '/sources', label: 'Sources', icon: Server },
  { to: '/pipeline', label: 'Pipeline', icon: Gauge },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export function Layout() {
  const { t } = useI18n()
  const auth = useAuth()
  const [open, setOpen] = useState(false)
  const location = useLocation()

  useEffect(() => setOpen(false), [location.pathname])

  return (
    <div className="app-shell">
      <aside className={`sidebar ${open ? 'sidebar-open' : ''}`}>
        <div className="brand">
          <span className="brand-mark"><Activity size={19} strokeWidth={2.2} /></span>
          <span>
            <strong>PULSE</strong>
            <small>{t('DNS Traffic Visibility')}</small>
          </span>
        </div>

        <nav className="primary-nav" aria-label={t('Primary navigation')}>
          {navigation.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}>
              <Icon size={17} />
              <span>{t(label)}</span>
            </NavLink>
          ))}
        </nav>

        <div className="sidebar-foot">
          <div><Database size={14} /><span>{auth.session?.user.username}<small>Admin</small></span></div>
          <button type="button" onClick={() => void auth.logout()} aria-label={t('Logout')} title={t('Logout')}><LogOut size={14} /></button>
        </div>
      </aside>

      {open && <button className="sidebar-scrim" aria-label={t('Close navigation')} onClick={() => setOpen(false)} />}

      <main className="main-shell">
        <div className="mobile-bar">
          <button className="icon-button" onClick={() => setOpen((value) => !value)} aria-label={t('Toggle navigation')}>
            {open ? <X size={20} /> : <Menu size={20} />}
          </button>
          <span className="mobile-brand">PULSE</span>
        </div>
        <Outlet />
      </main>
    </div>
  )
}
