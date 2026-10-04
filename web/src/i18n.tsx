import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Language = 'ru' | 'en'

export const LANGUAGE_STORAGE_KEY = 'pulse-language'

const ru: Record<string, string> = {
  'DNS Traffic Visibility': 'Мониторинг DNS-трафика',
  'Overview': 'Обзор', 'Live': 'Эфир', 'Clients': 'Клиенты', 'Domains': 'Домены', 'Search': 'Поиск',
  'Anomalies': 'Аномалии', 'Sources': 'Источники', 'Pipeline': 'Конвейер', 'Settings': 'Настройки',
  'Primary navigation': 'Основная навигация', 'Close navigation': 'Закрыть навигацию', 'Toggle navigation': 'Переключить навигацию',
  'Time range': 'Временной диапазон', 'Unable to load data': 'Не удалось загрузить данные', 'Retry': 'Повторить',
  'No data in this range': 'Нет данных за этот период', 'Try a wider time range or adjust the filters.': 'Расширьте период или измените фильтры.',
  'Loading': 'Загрузка', 'View all': 'Показать все', 'Pulse API is unreachable': 'Pulse API недоступен',
  'OPERATIONS': 'ЭКСПЛУАТАЦИЯ', 'ENTITIES': 'ОБЪЕКТЫ', 'INGESTION': 'СБОР ДАННЫХ', 'SYSTEM': 'СИСТЕМА',
  'REAL-TIME': 'РЕАЛЬНОЕ ВРЕМЯ', 'QUERY EXPLORER': 'ПОИСК ЗАПРОСОВ', 'CLIENT INSPECTOR': 'АНАЛИЗ КЛИЕНТА',
  'DOMAIN INSPECTOR': 'АНАЛИЗ ДОМЕНА', 'SOURCE': 'ИСТОЧНИК',
  'DNS overview': 'Обзор DNS', 'Traffic, reliability, and resolver activity across the selected window.': 'Трафик, надёжность и активность резолверов за выбранный период.',
  'Refresh failed — showing the most recent successful response.': 'Обновить не удалось — показан последний успешный ответ.',
  'Current QPS': 'Текущий QPS', 'last 60 seconds': 'последние 60 секунд', 'Queries today': 'Запросы сегодня',
  'Queries in range': 'Запросы за период', 'Resolvers': 'Резолверы', 'Data freshness': 'Свежесть данных',
  'Last event': 'Последнее событие', 'Ingest lag': 'Задержка приёма', '{count} healthy': 'исправно: {count}',
  'last seen': 'последнее событие', 'ago': 'назад',
  'vs previous {range}': 'к предыдущим {range}', 'vs previous 60s': 'к предыдущим 60 с',
  'queries today': 'запросов сегодня', 'unique clients': 'уникальных клиентов', 'unique domains': 'уникальных доменов',
  'Unique clients': 'Уникальные клиенты', 'Unique domains': 'Уникальные домены', 'P95 latency': 'Задержка P95',
  'Active sources': 'Активные источники', 'DNS traffic': 'DNS-трафик', 'Top domains': 'Топ доменов', 'Top clients': 'Топ клиентов',
  'Chart metric': 'Метрика графика', 'Traffic': 'Трафик', 'Errors': 'Ошибки', 'avg': 'среднее', 'latency': 'задержка',
  'Resolver health': 'Состояние резолверов', 'Current ingestion state with metrics for the selected range': 'Текущее состояние приёма и метрики за выбранный период',
  'Resolver comparison': 'Сравнение резолверов', '{range} metrics with realtime QPS': 'Метрики за {range}, QPS в реальном времени', 'Traffic share': 'Доля трафика',
  'RCODE distribution': 'Распределение RCODE', 'Responses in the selected range': 'Ответы за выбранный период',
  'Operational signals': 'Операционные сигналы', 'Current anomaly evaluation': 'Текущая оценка аномалий',
  'Highest query volume': 'Максимальный объём запросов', 'Response codes': 'Коды ответов', 'Distribution by response': 'Распределение по ответам',
  'Resolver telemetry': 'Телеметрия резолверов', 'Active anomalies': 'Активные аномалии', 'No active anomalies': 'Нет активных аномалий',
  'All monitored thresholds are currently within their configured bounds.': 'Все контролируемые пороги в заданных пределах.', 'All sources': 'Все источники',
  'Domain': 'Домен', 'Queries': 'Запросы', 'Client': 'Клиент', 'Source': 'Источник', 'Status': 'Статус', 'Time': 'Время',
  'Protocol': 'Протокол', 'Type': 'Тип', 'Outcome': 'Исход', 'Latency': 'Задержка', 'Answers': 'Ответы', 'Bytes': 'Байты',
  'Responses': 'Ответы', 'Avg latency': 'Средняя задержка', 'P99 latency': 'Задержка P99', 'Error rate': 'Доля ошибок',
  'No response': 'Без ответа', 'No client response observed within correlation window': 'Ответ клиента не получен в окне корреляции',
  'Waiting for DNS events': 'Ожидание DNS-событий', 'No matching events': 'Подходящие события не найдены',
  'The stream is connected. New matching events will appear here.': 'Поток подключён. Новые подходящие события появятся здесь.',
  'Try a wider range or remove one or more filters.': 'Расширьте период или уберите один или несколько фильтров.', 'Copy client': 'Скопировать клиента', 'Copy domain': 'Скопировать домен',
  'DNS consumers ranked by observed query volume.': 'DNS-клиенты по наблюдаемому объёму запросов.', 'Queried domains ranked by observed DNS volume.': 'Домены по наблюдаемому объёму DNS-трафика.',
  'Client IP': 'IP клиента', 'NXDOMAIN rate': 'Доля NXDOMAIN',
  'Live DNS events': 'DNS-события в эфире', 'Best-effort event stream from the active collector pipeline.': 'Поток событий из активного конвейера сборщика без гарантии доставки.',
  'Matched / sec': 'Совпало / с', 'Shown / sec': 'Показано / с', 'Suppressed': 'Отфильтровано', 'Queue dropped': 'Потеряно в очереди',
  'Rate limit': 'Лимит скорости', 'Subscribers': 'Подписчики', 'Stream filters': 'Фильтры потока', 'Filters are applied server-side when the stream reconnects.': 'Фильтры применяются на сервере при переподключении потока.',
  'Any source': 'Любой источник', 'Any client': 'Любой клиент', 'Any domain': 'Любой домен', 'Any type': 'Любой тип', 'Any code': 'Любой код', 'Any': 'Любой',
  'Slow threshold (ms)': 'Порог медленных (мс)', 'Disabled': 'Отключено', 'Investigation': 'Анализ', 'Failures only': 'Только ошибки', 'Rate / sec': 'Скорость / с',
  'Apply filters': 'Применить фильтры', 'Event stream': 'Поток событий', 'Newest first · browser buffer capped at 1,000 events': 'Сначала новые · буфер браузера до 1 000 событий',
  'Resume': 'Продолжить', 'Pause': 'Пауза', 'Auto-scroll': 'Автопрокрутка', 'on': 'вкл', 'off': 'выкл', 'Clear': 'Очистить',
  'Display paused — incoming events are temporarily ignored.': 'Отображение приостановлено — входящие события временно игнорируются.',
  'Historical search': 'Поиск по истории', 'Inspect retained raw DNS events. Results are ordered newest first.': 'Исследуйте сохранённые DNS-события. Сначала показаны новые.',
  'Investigate an IP or domain': 'Исследовать IP или домен', 'Open a focused Inspector; raw Query Explorer remains available below.': 'Откройте детальный анализ; поиск сырых событий доступен ниже.',
  'IP or domain': 'IP или домен', 'Investigate': 'Исследовать', 'Search filters': 'Фильтры поиска', 'Raw event retention is limited to 24 hours.': 'Сырые события хранятся не более 24 часов.',
  'Range': 'Период', 'Custom': 'Точный интервал', 'From': 'С', 'To': 'По', 'Timezone': 'Часовой пояс', 'Run search': 'Найти', 'DNS events': 'DNS-события', 'Searching…': 'Поиск…', 'No matching DNS events': 'Подходящие DNS-события не найдены', 'Loading…': 'Загрузка…', 'Load next page': 'Загрузить следующую страницу',
  'Raw event retention: {retention}.': 'Хранение сырых событий: {retention}.', 'Raw event retention is reported by backend.': 'Срок хранения сырых событий загружается с backend.',
  'From and To are required.': 'Укажите начало и конец интервала.', 'From must be earlier than To.': 'Время «С» должно быть раньше времени «По».', 'Invalid custom time range.': 'Некорректный временной интервал.',
  'The selected window is longer than raw event retention ({retention}).': 'Выбранный интервал длиннее срока хранения сырых событий ({retention}).',
  'This window is outside raw event retention; matching events may already have expired.': 'Этот интервал находится за пределами хранения сырых событий; данные уже могли быть удалены.',
  'Part of this window is outside raw event retention and may be incomplete.': 'Часть интервала находится за пределами хранения сырых событий; результат может быть неполным.',
  'Current threshold deviations. These are operational signals, not confirmed security incidents.': 'Текущие отклонения от порогов. Это эксплуатационные сигналы, а не подтверждённые инциденты.',
  'Current operational threshold state': 'Текущее состояние операционных порогов', 'All monitored thresholds are within their configured bounds.': 'Все контролируемые пороги в заданных пределах.',
  'Severity': 'Важность', 'Anomaly': 'Аномалия', 'Value': 'Значение', 'Threshold': 'Порог', 'Observed': 'Обнаружено',
  'All clients': 'Все клиенты', 'All domains': 'Все домены', 'No source in range': 'Нет источника за период',
  'DNS profile, failures, latency, history, and live trace.': 'DNS-профиль, ошибки, задержка, история и трассировка в эфире.', 'Clients, outcomes, latency, history, and live trace.': 'Клиенты, исходы, задержка, история и трассировка в эфире.',
  'Copy IP': 'Скопировать IP', 'Watch Live': 'Смотреть в эфире', 'matched responses': 'сопоставленные ответы', 'response data': 'данные ответов',
  'Client timeline': 'Хронология клиента', 'Domain timeline': 'Хронология домена', 'Domain behavior': 'Поведение доменов', 'Top queried domains from hourly aggregates': 'Топ доменов по часовым агрегатам',
  'No domain activity': 'Нет активности доменов', 'No client activity': 'Нет активности клиентов', 'Source context': 'Контекст источников',
  'Sources that observed this client in the selected range': 'Источники, наблюдавшие этого клиента за выбранный период', 'Unmatched responses': 'Несопоставленные ответы', 'Exact last seen': 'Точное время последнего появления',
  'Recent transactions': 'Недавние транзакции', 'All': 'Все', 'Failures': 'Ошибки', 'Slow': 'Медленные', 'No transactions for this filter': 'Нет транзакций для этого фильтра',
  'Clients querying this domain from hourly aggregates': 'Клиенты, запрашивающие этот домен, по часовым агрегатам', 'Query types and sources': 'Типы запросов и источники', 'Distribution in the selected window': 'Распределение за выбранный период',
  'Telemetry sources': 'Источники телеметрии', 'DNS telemetry producers observed by Pulse.': 'Источники DNS-телеметрии, наблюдаемые Pulse.', 'No configured or observed sources': 'Нет настроенных или наблюдаемых источников',
  'Source ID': 'ID источника', 'Last seen': 'Последнее появление', 'never': 'никогда',
  'Connection, ingestion, outcomes, and silence state.': 'Соединение, приём, исходы и состояние тишины.', 'Configuration': 'Конфигурация', 'Expected': 'Ожидаемый', 'Unexpected': 'Неожиданный',
  'Connection': 'Соединение', 'Established': 'Установлено', 'Disconnected': 'Отключено', 'Ingest rate': 'Скорость приёма', 'Runtime and persistence': 'Выполнение и хранение',
  'Remote address': 'Удалённый адрес', 'Last message': 'Последнее сообщение', 'Last persisted': 'Последняя запись', 'Silence age': 'Время тишины', 'Frames / queries / responses': 'Кадры / запросы / ответы',
  'Runtime errors': 'Ошибки выполнения', 'NXDOMAIN / unmatched': 'NXDOMAIN / несопоставленные',
  'Durable ingestion, live delivery, DNS sources, and ClickHouse capacity.': 'Надёжный приём, доставка в эфире, DNS-источники и ёмкость ClickHouse.', 'Insert rate': 'Скорость вставки', 'Pending': 'В ожидании', 'Durable dropped': 'Потери в хранилище',
  'Write failures': 'Ошибки записи', 'Raw rows': 'Сырые строки', 'compressed size unavailable': 'сжатый размер недоступен', 'Durable telemetry': 'Надёжная телеметрия',
  'Persistent queue': 'Очередь хранения', 'Queued / inserted': 'В очереди / вставлено', 'Batches': 'Пакеты', 'Correlation window': 'Окно корреляции', 'Pending persistence': 'Хранение ожидающих',
  'enabled': 'включено', 'disabled': 'отключено', 'Recovered / recovery errors': 'Восстановлено / ошибки', 'Persistence lag': 'Отставание записи', 'Last checkpoint': 'Последняя контрольная точка', 'not yet': 'ещё нет',
  'NO_RESPONSE meaning': 'Значение NO_RESPONSE', 'Live telemetry': 'Телеметрия в эфире', 'Best-effort; live loss does not imply durable history loss': 'Без гарантии; потери в эфире не означают потерю истории',
  'Publisher queue': 'Очередь публикатора', 'Sent / dropped': 'Отправлено / потеряно', 'UDP errors / decode errors': 'Ошибки UDP / декодирования', 'SSE subscribers': 'Подписчики SSE',
  'DNS sources': 'DNS-источники', 'A TCP session alone does not prove telemetry flow': 'Сама по себе TCP-сессия не подтверждает поток телеметрии', 'State': 'Состояние',
  'Storage': 'Хранилище', 'Filesystem path unavailable': 'Путь файловой системы недоступен', 'Filesystem used / total': 'Файловая система: занято / всего', 'unavailable': 'недоступно',
  'Utilization': 'Использование', 'Warning / critical': 'Предупреждение / критический', 'Pulse compressed': 'Сжатые данные Pulse', 'Active parts / merges': 'Активные части / слияния',
  'Oldest raw event': 'Самое старое сырое событие', 'Newest raw event': 'Самое новое сырое событие', 'Filesystem free': 'Свободно в файловой системе',
  'Runtime capabilities reported by the Pulse API.': 'Возможности runtime, сообщаемые Pulse API.', 'Language': 'Язык', 'Interface language': 'Язык интерфейса', 'Russian': 'Русский', 'English': 'English',
  'Language preference is saved in this browser.': 'Выбранный язык сохраняется в этом браузере.', 'API status': 'Статус API', 'Live runtime state': 'Текущее состояние runtime',
  'Service': 'Сервис', 'API version': 'Версия API', 'Server time': 'Время сервера', 'Live transport': 'Транспорт эфира', 'Live rate': 'Скорость эфира', 'default': 'по умолчанию', 'max': 'макс.',
  'Data retention': 'Хранение данных', 'Reported by the backend': 'По данным backend', 'Capabilities': 'Возможности', 'Available API functions': 'Доступные функции API',
  'Page not found': 'Страница не найдена', 'The requested Pulse view does not exist.': 'Запрошенный раздел Pulse не существует.', 'Return to overview': 'Вернуться к обзору',
  'queries': 'запросов', 'avg latency': 'средняя задержка', 'normalized transactions': 'нормализованные транзакции', 'recent process sample': 'последняя выборка процесса',
  'peak {count}': 'пик {count}', 'since {time}': 'с {time}', 'unknown remote': 'удалённый адрес неизвестен',
  '{count} active · {window} evaluation window': '{count} активных · окно оценки {window}', '{count} buffered': 'в буфере: {count}', '{count} events loaded · cursor pagination': 'загружено событий: {count} · курсорная пагинация',
  '{count} in range': '{count} за период', '{count} responses': 'ответов: {count}', '{count} seen in range': 'видно за период: {count}', '{count} unique clients': 'уникальных клиентов: {count}', '{count} unique domains': 'уникальных доменов: {count}',
  '{range} query window · status is derived from last event time': 'окно запросов {range} · статус определяется по времени последнего события', '{range} window · up to 100 clients': 'окно {range} · до 100 клиентов', '{range} window · up to 100 domains': 'окно {range} · до 100 доменов',
  '{range} window · {count} total queries': 'окно {range} · всего запросов: {count}', '{range} · volume and outcome/latency context': '{range} · объём, исходы и задержка', '{sources} · last seen {time}': '{sources} · последнее появление {time}', '{window} operational window': 'операционное окно {window}',
  'Counters since collector start · analytics over {range}': 'счётчики с запуска сборщика · аналитика за {range}', 'Counters since collector start · uptime {uptime}': 'счётчики с запуска сборщика · uptime {uptime}',
  'Failures = NO_RESPONSE, SERVFAIL, REFUSED, or FORMERR; NXDOMAIN is separate · raw {range}': 'Ошибки = NO_RESPONSE, SERVFAIL, REFUSED или FORMERR; NXDOMAIN учитывается отдельно · сырые данные {range}',
  'expected': 'ожидаемый', 'unexpected': 'неожиданный', 'established': 'установлено', 'disconnected': 'отключено',
  'Elevated NXDOMAIN rate': 'Повышенная доля NXDOMAIN', 'Elevated SERVFAIL rate': 'Повышенная доля SERVFAIL', 'Client responses not observed': 'Ответы клиента не наблюдаются', 'Elevated DNS response latency': 'Повышенная задержка DNS-ответов', 'DNS source is silent': 'DNS-источник молчит',
  'high nxdomain rate': 'высокая доля NXDOMAIN', 'high servfail rate': 'высокая доля SERVFAIL', 'high no response rate': 'высокая доля без ответа', 'high average latency': 'высокая средняя задержка', 'source silent': 'источник молчит',
  'percent': '%', 'minutes': 'мин',
  'raw events': 'сырые события', 'minute aggregates': 'минутные агрегаты', 'hourly aggregates': 'часовые агрегаты', 'last seen state': 'состояние last seen',
  'historical search': 'поиск по истории', 'live stream': 'поток в эфире', 'anomalies': 'аномалии', 'source health': 'состояние источников', 'pipeline health': 'состояние конвейера', 'client inspector': 'анализ клиента', 'domain inspector': 'анализ домена',
  'client domain hour': 'домены клиентов (час)', 'client latency minute': 'задержка клиентов (минута)', 'client minute': 'клиенты (минута)', 'domain latency minute': 'задержка доменов (минута)',
  'domain minute': 'домены (минута)', 'latency minute': 'задержка (минута)', 'raw': 'сырые события', 'rcode minute': 'RCODE (минута)', 'traffic minute': 'трафик (минута)',
  'client detail': 'детали клиента', 'client domains': 'домены клиента', 'cursor search': 'курсорный поиск', 'domain clients': 'клиенты домена', 'domain detail': 'детали домена',
  'live outcome filters': 'фильтры исходов в эфире', 'live sse': 'эфир SSE', 'multi source': 'несколько источников', 'multi tenant': 'мультитенантность', 'overview': 'обзор', 'search': 'поиск',
  'source detail': 'детали источника', 'system': 'система', 'transaction outcomes': 'исходы транзакций',
  'DNS access control': 'Управление DNS-доступом', 'Applies only to DNS traffic on TCP/UDP port 53.': 'Применяется только к DNS-трафику TCP/UDP на порту 53.',
  'Current status': 'Текущий статус', 'Reason': 'Причина', 'Expires': 'Истекает', 'Permanent': 'Бессрочно', 'Apply result': 'Результат применения',
  'Block duration': 'Срок блокировки', 'Why is this client being blocked?': 'Почему этот клиент блокируется?', 'Block DNS': 'Заблокировать DNS', 'Unblock DNS': 'Разблокировать DNS',
  'Confirm blocking DNS for {ip} for {duration}. This is a real firewall action.': 'Подтвердите блокировку DNS для {ip} на {duration}. Это реальное изменение firewall.', 'Confirm unblocking DNS for {ip}.': 'Подтвердите разблокировку DNS для {ip}.',
  'Applying…': 'Применение…', 'Confirm': 'Подтвердить', 'Cancel': 'Отмена', 'firewall state applied': 'Состояние firewall применено',
  'Desired state': 'Желаемое состояние', 'Convergence': 'Сходимость', 'DNS node': 'DNS-узел', 'Agent health': 'Состояние агента', 'Control': 'Управление',
  'Apply status': 'Статус применения', 'Result': 'Результат', 'Telemetry': 'Телеметрия', 'Agent': 'Агент',
  '{count} attempts': 'попыток: {count}', 'Retry at {time}': 'Повтор в {time}', 'dry-run': 'тестовый режим', 'enforcing': 'боевой режим',
  'DNS control nodes': 'Узлы DNS-управления', 'Authenticated agents and firewall control mode': 'Аутентифицированные агенты и режим управления firewall',
  'No control nodes configured': 'Узлы управления не настроены', 'Mode': 'Режим', 'DNS service IP': 'IP DNS-сервиса',
  'SECURE OPERATIONS CONSOLE': 'ЗАЩИЩЁННАЯ КОНСОЛЬ УПРАВЛЕНИЯ', 'Securing session…': 'Защищаем сессию…',
  'ADMIN ACCESS': 'ДОСТУП АДМИНИСТРАТОРА', 'Welcome to Pulse': 'Добро пожаловать в Pulse', 'Authenticate to enter the DNS operations workspace.': 'Войдите в защищённое пространство управления DNS.',
  'Username': 'Имя пользователя', 'Password': 'Пароль', 'Login failed': 'Ошибка входа', 'Authenticating…': 'Проверка…', 'Continue': 'Продолжить',
  'Protected with Argon2id and secure sessions': 'Защищено Argon2id и безопасными сессиями', 'Logout': 'Выйти',
  'Users': 'Пользователи', 'Admin accounts and access lifecycle': 'Учётные записи администраторов и управление доступом', 'Initial password': 'Начальный пароль', 'Create Admin': 'Создать Admin',
  'Admin': 'Администратор', 'Access': 'Доступ', 'Created': 'Создан', 'Actions': 'Действия', 'current session': 'текущая сессия',
  'Active': 'Включён', 'Inactive': 'Отключён', 'Reset password': 'Сбросить пароль', 'New password': 'Новый пароль', 'Save': 'Сохранить',
  'Revoke sessions': 'Отозвать сессии', 'Disable': 'Отключить', 'Enable': 'Включить', 'Delete': 'Удалить',
  'Revoke all active sessions?': 'Отозвать все активные сессии?', 'Delete this Admin permanently?': 'Навсегда удалить этого администратора?',
  'Admin created': 'Администратор создан', 'Password reset; active sessions revoked': 'Пароль сброшен; активные сессии отозваны',
  'Sessions revoked': 'Сессии отозваны', 'Admin disabled': 'Администратор отключён', 'Admin enabled': 'Администратор включён', 'Admin deleted': 'Администратор удалён', 'Operation failed': 'Операция не выполнена',
}

const statusRu: Record<string, string> = {
  active: 'активен', ok: 'норма', connected: 'подключён', live: 'в эфире', healthy: 'исправен', response: 'ответ',
  warning: 'предупреждение', delayed: 'задержка', connecting: 'подключение', reconnecting: 'переподключение', degraded: 'деградация',
  connected_no_events: 'подключён, нет событий', unexpected: 'неожиданный', critical: 'критический', silent: 'молчит', error: 'ошибка',
  offline: 'не в сети', disconnected: 'отключён', never_seen_since_start: 'не наблюдался с запуска', no_response: 'нет ответа', unmatched_response: 'несопоставленный ответ',
  expected: 'ожидаемый', established: 'установлено', enabled: 'включено', disabled: 'отключено', unavailable: 'недоступно',
  pending: 'ожидает применения', applied: 'применено', blocked: 'заблокирован', unblocked: 'разблокирован',
  partial: 'частично применено', unknown: 'неизвестно',
}

interface I18nValue {
  language: Language
  locale: string
  setLanguage: (language: Language) => void
  t: (key: string, values?: Record<string, string | number>) => string
  statusLabel: (value: string) => string
}

const I18nContext = createContext<I18nValue | null>(null)

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState<Language>(() => localStorage.getItem(LANGUAGE_STORAGE_KEY) === 'en' ? 'en' : 'ru')

  useEffect(() => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, language)
    document.documentElement.lang = language
  }, [language])

  const value = useMemo<I18nValue>(() => {
    const t = (key: string, values: Record<string, string | number> = {}) => {
      const template = language === 'ru' ? (ru[key] ?? key) : key
      return Object.entries(values).reduce((result, [name, replacement]) => result.replaceAll(`{${name}}`, String(replacement)), template)
    }
    return {
      language,
      locale: language === 'ru' ? 'ru-RU' : 'en-GB',
      setLanguage: (nextLanguage) => {
        // Persist before rendering so locale-aware formatters use the new locale immediately.
        localStorage.setItem(LANGUAGE_STORAGE_KEY, nextLanguage)
        setLanguage(nextLanguage)
      },
      t,
      statusLabel: (status) => language === 'ru' ? (statusRu[status.toLowerCase()] ?? status) : status,
    }
  }, [language])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

// The provider and its companion hook intentionally share one module.
// eslint-disable-next-line react-refresh/only-export-components
export function useI18n(): I18nValue {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n must be used within LanguageProvider')
  return value
}
