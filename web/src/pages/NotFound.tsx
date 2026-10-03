import { Link } from 'react-router-dom'
import { useI18n } from '../i18n'

export function NotFoundPage() {
  const { t } = useI18n()
  return <div className="page not-found"><span>404</span><h1>{t('Page not found')}</h1><p>{t('The requested Pulse view does not exist.')}</p><Link className="button primary" to="/">{t('Return to overview')}</Link></div>
}
