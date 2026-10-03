import { KeyRound, Plus, Trash2, UserCheck, UserX, X } from 'lucide-react'
import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, type AdminUser } from '../api'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'
import { Panel } from './Common'

export function UsersPanel() {
  const { t } = useI18n()
  const auth = useAuth()
  const [users, setUsers] = useState<AdminUser[]>([])
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [resetUser, setResetUser] = useState<string | null>(null)
  const [resetPassword, setResetPassword] = useState('')
  const [busy, setBusy] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    const response = await api.adminUsers()
    setUsers(response.users)
  }, [])

  useEffect(() => { void load().catch((reason: Error) => setError(reason.message)) }, [load])

  async function action(name: string, run: () => Promise<unknown>, success: string, refreshAuth = false) {
    setBusy(name)
    setError('')
    setMessage('')
    try {
      await run()
      setMessage(success)
      await load()
      if (refreshAuth) await auth.refresh()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('Operation failed'))
    } finally {
      setBusy('')
    }
  }

  async function create(event: FormEvent) {
    event.preventDefault()
    await action('create', () => api.createAdmin(username, password), t('Admin created'))
    setUsername('')
    setPassword('')
  }

  async function submitPassword(event: FormEvent) {
    event.preventDefault()
    if (!resetUser) return
    const self = resetUser === auth.session?.user.username
    await action(`password:${resetUser}`, () => api.resetAdminPassword(resetUser, resetPassword), t('Password reset; active sessions revoked'), self)
    setResetUser(null)
    setResetPassword('')
  }

  return (
    <Panel title={t('Users')} subtitle={t('Admin accounts and access lifecycle')} className="span-two users-panel">
      <form className="admin-create" onSubmit={create}>
        <label><span>{t('Username')}</span><input value={username} onChange={(event) => setUsername(event.target.value)} minLength={3} maxLength={64} autoComplete="off" required /></label>
        <label><span>{t('Initial password')}</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} minLength={12} maxLength={512} autoComplete="new-password" required /></label>
        <button className="button primary" type="submit" disabled={busy !== ''}><Plus size={14} />{t('Create Admin')}</button>
      </form>
      {(message || error) && <div className={error ? 'admin-notice error' : 'admin-notice'} role="status">{error || message}</div>}
      {resetUser && <form className="password-reset-strip" onSubmit={submitPassword}><KeyRound size={15} /><strong>{t('Reset password')}: {resetUser}</strong><input type="password" value={resetPassword} onChange={(event) => setResetPassword(event.target.value)} minLength={12} placeholder={t('New password')} autoFocus required /><button className="button primary" disabled={busy !== ''}>{t('Save')}</button><button className="icon-button" type="button" onClick={() => setResetUser(null)} aria-label={t('Cancel')}><X size={15} /></button></form>}
      <div className="table-scroll">
        <table className="data-table admin-table"><thead><tr><th>{t('Admin')}</th><th>{t('Access')}</th><th>{t('Created')}</th><th className="align-right">{t('Actions')}</th></tr></thead><tbody>
          {users.map((user) => <tr key={user.username}>
            <td><strong>{user.username}</strong>{user.username === auth.session?.user.username && <small>{t('current session')}</small>}</td>
            <td><span className={user.enabled ? 'admin-state enabled' : 'admin-state'}>{user.enabled ? <UserCheck size={13} /> : <UserX size={13} />}{t(user.enabled ? 'Active' : 'Inactive')}</span></td>
            <td className="mono">{new Date(user.created_at).toLocaleDateString()}</td>
            <td><div className="admin-actions">
              <button className="button secondary" onClick={() => setResetUser(user.username)} disabled={busy !== ''} type="button"><KeyRound size={13} />{t('Password')}</button>
              <button className="button secondary" onClick={() => window.confirm(t('Revoke all active sessions?')) && void action(`sessions:${user.username}`, () => api.revokeAdminSessions(user.username), t('Sessions revoked'), user.username === auth.session?.user.username)} disabled={busy !== ''} type="button">{t('Revoke sessions')}</button>
              <button className="button secondary" onClick={() => void action(`enabled:${user.username}`, () => api.setAdminEnabled(user.username, !user.enabled), t(user.enabled ? 'Admin disabled' : 'Admin enabled'), user.enabled && user.username === auth.session?.user.username)} disabled={busy !== ''} type="button">{t(user.enabled ? 'Disable' : 'Enable')}</button>
              <button className="button danger" onClick={() => window.confirm(t('Delete this Admin permanently?')) && void action(`delete:${user.username}`, () => api.deleteAdmin(user.username), t('Admin deleted'), user.username === auth.session?.user.username)} disabled={busy !== ''} type="button" aria-label={`${t('Delete')} ${user.username}`}><Trash2 size={13} /></button>
            </div></td>
          </tr>)}
        </tbody></table>
      </div>
    </Panel>
  )
}
