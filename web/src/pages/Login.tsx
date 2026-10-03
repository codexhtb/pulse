import { Activity, ArrowRight, Languages, LockKeyhole, UserRound } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { ApiError } from '../api'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'

export function LoginPage() {
  const { language, setLanguage, t } = useI18n()
  const auth = useAuth()
  const location = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!auth.loading && auth.session?.state === 'authenticated') {
    const requested = (location.state as { from?: string } | null)?.from
    return <Navigate to={requested && requested !== '/login' ? requested : '/'} replace />
  }

  async function submitLogin(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await auth.login(username, password)
      setPassword('')
    } catch (reason) {
      setError(reason instanceof ApiError ? reason.message : t('Login failed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="login-scene">
      <div className="login-atmosphere" aria-hidden="true" />
      <div className="pulse-identity" aria-hidden="true">
        <svg viewBox="0 0 980 210" role="presentation">
          <defs>
            <linearGradient id="pulse-line" x1="0" x2="1">
              <stop offset="0" stopColor="#17667a" stopOpacity="0" />
              <stop offset=".26" stopColor="#45c9e8" />
              <stop offset=".74" stopColor="#80e0f0" />
              <stop offset="1" stopColor="#17667a" stopOpacity="0" />
            </linearGradient>
            <filter id="pulse-glow"><feGaussianBlur stdDeviation="5" result="blur" /><feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge></filter>
          </defs>
          <path className="heartbeat-glow" pathLength="1" d="M0 100 H132 L147 100 L157 82 L168 119 L181 54 L196 137 L211 100 H350 L363 100 L372 88 L381 111 L392 70 L404 125 L417 100 H563 L575 100 L584 86 L594 114 L605 66 L618 128 L631 100 H980" />
          <path className="heartbeat-line" pathLength="1" d="M0 100 H132 L147 100 L157 82 L168 119 L181 54 L196 137 L211 100 H350 L363 100 L372 88 L381 111 L392 70 L404 125 L417 100 H563 L575 100 L584 86 L594 114 L605 66 L618 128 L631 100 H980" />
          <text className="pulse-word" x="490" y="128" textAnchor="middle">PULSE</text>
          <text className="pulse-signature" x="603" y="166" textAnchor="middle">by.Bezhan</text>
        </svg>
      </div>

      <section className="login-card">
        <header className="login-card-header">
          <span className="login-emblem"><Activity size={21} /></span>
          <div><strong>PULSE</strong><small>{t('SECURE OPERATIONS CONSOLE')}</small></div>
          <button className="login-language" type="button" onClick={() => setLanguage(language === 'ru' ? 'en' : 'ru')} aria-label={t('Interface language')}>
            <Languages size={15} /> {language === 'ru' ? 'EN' : 'RU'}
          </button>
        </header>

        {auth.loading ? <div className="login-loading"><i /><span>{t('Securing session…')}</span></div> : (
          <form className="login-form login-step" onSubmit={submitLogin}>
            <div className="login-copy"><span>{t('ADMIN ACCESS')}</span><h1>{t('Welcome to Pulse')}</h1><p>{t('Authenticate to enter the DNS operations workspace.')}</p></div>
            <label className="login-field"><span>{t('Username')}</span><div><UserRound size={16} /><input name="username" value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" autoFocus required /></div></label>
            <label className="login-field"><span>{t('Password')}</span><div><LockKeyhole size={16} /><input name="password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required /></div></label>
            {error && <div className="login-error" role="alert">{error}</div>}
            <button className="login-submit" disabled={busy} type="submit"><span>{busy ? t('Authenticating…') : t('Continue')}</span><ArrowRight size={16} /></button>
          </form>
        )}

        <footer className="login-card-footer"><i /><span>{t('Protected with Argon2id and secure sessions')}</span></footer>
      </section>
    </main>
  )
}
