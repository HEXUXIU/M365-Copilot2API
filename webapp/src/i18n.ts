// Console i18n dictionary. Keys are English source strings; values map to
// each supported locale. Missing keys fall back to the source string.
export type Locale =
  | "en"
  | "zh-CN"
  | "zh-TW"
  | "ja"
  | "ko"
  | "es"
  | "fr"
  | "de"
  | "pt-BR"
  | "ru"
  | "ar";

export const LOCALES: { id: Locale; label: string }[] = [
  { id: "en", label: "English" },
  { id: "zh-CN", label: "简体中文" },
  { id: "zh-TW", label: "繁體中文" },
  { id: "ja", label: "日本語" },
  { id: "ko", label: "한국어" },
  { id: "es", label: "Español" },
  { id: "fr", label: "Français" },
  { id: "de", label: "Deutsch" },
  { id: "pt-BR", label: "Português (Brasil)" },
  { id: "ru", label: "Русский" },
  { id: "ar", label: "العربية" },
];

const dict: Record<string, Partial<Record<Locale, string>>> = {
  // Navigation
  Dashboard: { "zh-CN": "控制台", "zh-TW": "控制台", ja: "ダッシュボード", ko: "대시보드", es: "Panel", fr: "Tableau de bord", de: "Dashboard", "pt-BR": "Painel", ru: "Панель", ar: "لوحة التحكم" },
  Usage: { "zh-CN": "用量", "zh-TW": "用量", ja: "使用量", ko: "사용량", es: "Uso", fr: "Utilisation", de: "Nutzung", "pt-BR": "Uso", ru: "Использование", ar: "الاستخدام" },
  Accounts: { "zh-CN": "账号", "zh-TW": "帳號", ja: "アカウント", ko: "계정", es: "Cuentas", fr: "Comptes", de: "Konten", "pt-BR": "Contas", ru: "Аккаунты", ar: "الحسابات" },
  "API Keys": { "zh-CN": "API 密钥", "zh-TW": "API 金鑰", ja: "APIキー", ko: "API 키", es: "Claves API", fr: "Clés API", de: "API-Schlüssel", "pt-BR": "Chaves de API", ru: "API-ключи", ar: "مفاتيح API" },
  Conversations: { "zh-CN": "对话管理", "zh-TW": "對話管理", ja: "会話管理", ko: "대화 관리", es: "Conversaciones", fr: "Conversations", de: "Unterhaltungen", "pt-BR": "Conversas", ru: "Диалоги", ar: "المحادثات" },
  "Proxy Pool": { "zh-CN": "代理池", "zh-TW": "代理池", ja: "プロキシプール", ko: "프록시 풀", es: "Pool de proxies", fr: "Pool de proxys", de: "Proxy-Pool", "pt-BR": "Pool de proxies", ru: "Пул прокси", ar: "مجموع الوكلاء" },
  "Model Test": { "zh-CN": "模型测试", "zh-TW": "模型測試", ja: "モデルテスト", ko: "모델 테스트", es: "Prueba de modelos", fr: "Test de modèles", de: "Modelltest", "pt-BR": "Teste de modelos", ru: "Тест моделей", ar: "اختبار النماذج" },
  Settings: { "zh-CN": "设置", "zh-TW": "設定", ja: "設定", ko: "설정", es: "Ajustes", fr: "Paramètres", de: "Einstellungen", "pt-BR": "Configurações", ru: "Настройки", ar: "الإعدادات" },
  "Log out": { "zh-CN": "退出登录", "zh-TW": "登出", ja: "ログアウト", ko: "로그아웃", es: "Cerrar sesión", fr: "Déconnexion", de: "Abmelden", "pt-BR": "Sair", ru: "Выйти", ar: "تسجيل الخروج" },
  Running: { "zh-CN": "运行中", "zh-TW": "執行中", ja: "実行中", ko: "실행 중", es: "En ejecución", fr: "En cours", de: "Läuft", "pt-BR": "Em execução", ru: "Работает", ar: "قيد التشغيل" },

  // Accounts page
  "Authorized accounts": { "zh-CN": "已授权账号", "zh-TW": "已授權帳號", ja: "承認済みアカウント", ko: "인증된 계정", es: "Cuentas autorizadas", fr: "Comptes autorisés", de: "Autorisierte Konten", "pt-BR": "Contas autorizadas", ru: "Авторизованные аккаунты", ar: "الحسابات المصرح بها" },
  "Select all": { "zh-CN": "全选", "zh-TW": "全選", ja: "すべて選択", ko: "모두 선택", es: "Seleccionar todo", fr: "Tout sélectionner", de: "Alle auswählen", "pt-BR": "Selecionar tudo", ru: "Выбрать все", ar: "تحديد الكل" },
  "0 selected": { "zh-CN": "已选 0 项", "zh-TW": "已選 0 項", ja: "0件選択", ko: "0개 선택", es: "0 seleccionados", fr: "0 sélectionné", de: "0 ausgewählt", "pt-BR": "0 selecionados", ru: "Выбрано 0", ar: "0 محدد" },
  "Enable sched": { "zh-CN": "启用调度", "zh-TW": "啟用調度", ja: "スケジュール有効", ko: "스케줄 허용", es: "Activar programación", fr: "Activer la planification", de: "Planung aktivieren", "pt-BR": "Ativar agendamento", ru: "Вкл. планирование", ar: "تمكين الجدولة" },
  "Disable sched": { "zh-CN": "停用调度", "zh-TW": "停用調度", ja: "スケジュール無効", ko: "스케줄 차단", es: "Desactivar programación", fr: "Désactiver la planification", de: "Planung deaktivieren", "pt-BR": "Desativar agendamento", ru: "Выкл. планирование", ar: "تعطيل الجدولة" },
  "Search on": { "zh-CN": "开启搜索", "zh-TW": "開啟搜尋", ja: "検索オン", ko: "검색 켜기", es: "Búsqueda on", fr: "Recherche on", de: "Suche an", "pt-BR": "Busca on", ru: "Поиск вкл", ar: "تشغيل البحث" },
  "Search off": { "zh-CN": "关闭搜索", "zh-TW": "關閉搜尋", ja: "検索オフ", ko: "검색 끄기", es: "Búsqueda off", fr: "Recherche off", de: "Suche aus", "pt-BR": "Busca off", ru: "Поиск выкл", ar: "إيقاف البحث" },
  "Apply prompt": { "zh-CN": "应用提示词", "zh-TW": "套用提示詞", ja: "プロンプト適用", ko: "프롬프트 적용", es: "Aplicar prompt", fr: "Appliquer le prompt", de: "Prompt anwenden", "pt-BR": "Aplicar prompt", ru: "Применить промпт", ar: "تطبيق المطالبة" },
  "Clear prompt": { "zh-CN": "清除提示词", "zh-TW": "清除提示詞", ja: "プロンプト解除", ko: "프롬프트 지우기", es: "Borrar prompt", fr: "Effacer le prompt", de: "Prompt löschen", "pt-BR": "Limpar prompt", ru: "Очистить промпт", ar: "مسح المطالبة" },
  "System prompt for selected…": { "zh-CN": "所选账号的系统提示词…", "zh-TW": "所選帳號的系統提示詞…", ja: "選択したアカウントのシステムプロンプト…", ko: "선택한 계정의 시스템 프롬프트…", es: "Prompt del sistema para seleccionados…", fr: "Prompt système pour la sélection…", de: "Systemprompt für Auswahl…", "pt-BR": "Prompt do sistema para selecionados…", ru: "Системный промпт для выбранных…", ar: "مطالبة النظام للمحدد…" },
  Enabled: { "zh-CN": "已启用", "zh-TW": "已啟用", ja: "有効", ko: "활성", es: "Activado", fr: "Activé", de: "Aktiviert", "pt-BR": "Ativado", ru: "Включено", ar: "ممكن" },
  Disabled: { "zh-CN": "已停用", "zh-TW": "已停用", ja: "無効", ko: "비활성", es: "Desactivado", fr: "Désactivé", de: "Deaktiviert", "pt-BR": "Desativado", ru: "Отключено", ar: "معطل" },
  Bind: { "zh-CN": "绑定", "zh-TW": "綁定", ja: "バインド", ko: "바인딩", es: "Vincular", fr: "Lier", de: "Binden", "pt-BR": "Vincular", ru: "Привязать", ar: "ربط" },
  Delete: { "zh-CN": "删除", "zh-TW": "刪除", ja: "削除", ko: "삭제", es: "Eliminar", fr: "Supprimer", de: "Löschen", "pt-BR": "Excluir", ru: "Удалить", ar: "حذف" },
  Online: { "zh-CN": "在线", "zh-TW": "線上", ja: "オンライン", ko: "온라인", es: "En línea", fr: "En ligne", de: "Online", "pt-BR": "Online", ru: "Онлайн", ar: "متصل" },
  Offline: { "zh-CN": "离线", "zh-TW": "離線", ja: "オフライン", ko: "오프라인", es: "Desconectado", fr: "Hors ligne", de: "Offline", "pt-BR": "Offline", ru: "Оффлайн", ar: "غير متصل" },
  Cooldown: { "zh-CN": "冷却中", "zh-TW": "冷卻中", ja: "クールダウン", ko: "쿨다운", es: "Enfriando", fr: "En attente", de: "Abklingzeit", "pt-BR": "Resfriando", ru: "Ожидание", ar: "تهدئة" },
  "No search": { "zh-CN": "无搜索", "zh-TW": "無搜尋", ja: "検索なし", ko: "검색 없음", es: "Sin búsqueda", fr: "Sans recherche", de: "Ohne Suche", "pt-BR": "Sem busca", ru: "Без поиска", ar: "بدون بحث" },
  "Custom prompt": { "zh-CN": "自定义提示词", "zh-TW": "自訂提示詞", ja: "カスタムプロンプト", ko: "사용자 프롬프트", es: "Prompt personalizado", fr: "Prompt personnalisé", de: "Eigener Prompt", "pt-BR": "Prompt personalizado", ru: "Свой промпт", ar: "مطالبة مخصصة" },

  // Settings page
  "Requests & models": { "zh-CN": "请求与模型", "zh-TW": "請求與模型", ja: "リクエストとモデル", ko: "요청과 모델", es: "Solicitudes y modelos", fr: "Requêtes et modèles", de: "Anfragen & Modelle", "pt-BR": "Solicitações e modelos", ru: "Запросы и модели", ar: "الطلبات والنماذج" },
  "Accounts & limits": { "zh-CN": "账号与限流", "zh-TW": "帳號與限流", ja: "アカウントと制限", ko: "계정과 제한", es: "Cuentas y límites", fr: "Comptes et limites", de: "Konten & Limits", "pt-BR": "Contas e limites", ru: "Аккаунты и лимиты", ar: "الحسابات والحدود" },
  "Feature flags": { "zh-CN": "功能开关", "zh-TW": "功能開關", ja: "機能フラグ", ko: "기능 플래그", es: "Banderas de funciones", fr: "Options", de: "Funktionen", "pt-BR": "Opções", ru: "Функции", ar: "الخصائص" },
  "Model mappings": { "zh-CN": "模型映射", "zh-TW": "模型映射", ja: "モデルマッピング", ko: "모델 매핑", es: "Mapeo de modelos", fr: "Mappage de modèles", de: "Modell-Zuordnung", "pt-BR": "Mapeamento de modelos", ru: "Маппинг моделей", ar: "تعيين النماذج" },
  System: { "zh-CN": "系统", "zh-TW": "系統", ja: "システム", ko: "시스템", es: "Sistema", fr: "Système", de: "System", "pt-BR": "Sistema", ru: "Система", ar: "النظام" },
  "Public model": { "zh-CN": "公开模型名", "zh-TW": "公開模型名", ja: "公開モデル名", ko: "공개 모델명", es: "Modelo público", fr: "Modèle public", de: "Öffentliches Modell", "pt-BR": "Modelo público", ru: "Публичная модель", ar: "النموذج العام" },
  "Upstream tone": { "zh-CN": "上游 tone", "zh-TW": "上游 tone", ja: "上流トーン", ko: "업스트림 톤", es: "Tono upstream", fr: "Tone upstream", de: "Upstream-Tone", "pt-BR": "Tone upstream", ru: "Upstream tone", ar: "نبرة المنبع" },
  "Display name": { "zh-CN": "显示名称", "zh-TW": "顯示名稱", ja: "表示名", ko: "표시 이름", es: "Nombre visible", fr: "Nom affiché", de: "Anzeigename", "pt-BR": "Nome de exibição", ru: "Отображаемое имя", ar: "اسم العرض" },
  Reasoning: { "zh-CN": "推理等级", "zh-TW": "推理等級", ja: "推論レベル", ko: "추론 수준", es: "Razonamiento", fr: "Raisonnement", de: "Denkstufe", "pt-BR": "Raciocínio", ru: "Рассуждение", ar: "الاستدلال" },
  Add: { "zh-CN": "添加", "zh-TW": "新增", ja: "追加", ko: "추가", es: "Añadir", fr: "Ajouter", de: "Hinzufügen", "pt-BR": "Adicionar", ru: "Добавить", ar: "إضافة" },
  "Save settings": { "zh-CN": "保存设置", "zh-TW": "儲存設定", ja: "設定を保存", ko: "설정 저장", es: "Guardar ajustes", fr: "Enregistrer", de: "Speichern", "pt-BR": "Salvar", ru: "Сохранить", ar: "حفظ" },
  Reload: { "zh-CN": "重新加载", "zh-TW": "重新載入", ja: "再読み込み", ko: "다시 불러오기", es: "Recargar", fr: "Recharger", de: "Neu laden", "pt-BR": "Recarregar", ru: "Обновить", ar: "إعادة تحميل" },
  "Auto start": { "zh-CN": "开机自启动（当前用户）", "zh-TW": "開機自啟動（目前使用者）", ja: "自動起動（現在のユーザー）", ko: "자동 시작(현재 사용자)", es: "Autoarranque (usuario actual)", fr: "Démarrage auto (utilisateur)", de: "Autostart (aktueller Nutzer)", "pt-BR": "Início automático (usuário)", ru: "Автозапуск (текущий пользователь)", ar: "التشغيل التلقائي (المستخدم)" },
  "Log location": { "zh-CN": "日志位置", "zh-TW": "日誌位置", ja: "ログの場所", ko: "로그 위치", es: "Ubicación de registros", fr: "Emplacement des journaux", de: "Log-Speicherort", "pt-BR": "Local dos logs", ru: "Расположение журналов", ar: "موقع السجلات" },
  "Listen address (restart required)": { "zh-CN": "监听地址（需重启）", "zh-TW": "監聽位址（需重啟）", ja: "リッスンアドレス（要再起動）", ko: "수신 주소(재시작 필요)", es: "Dirección de escucha (requiere reinicio)", fr: "Adresse d'écoute (redémarrage requis)", de: "Listen-Adresse (Neustart nötig)", "pt-BR": "Endereço de escuta (requer reinício)", ru: "Адрес прослушивания (нужен перезапуск)", ar: "عنوان الاستماع (يتطلب إعادة تشغيل)" },
  Language: { "zh-CN": "语言", "zh-TW": "語言", ja: "言語", ko: "언어", es: "Idioma", fr: "Langue", de: "Sprache", "pt-BR": "Idioma", ru: "Язык", ar: "اللغة" },

  // Common statuses / hints
  "No matching accounts": { "zh-CN": "没有匹配的账号", "zh-TW": "沒有符合的帳號", ja: "該当するアカウントなし", ko: "일치하는 계정 없음", es: "Sin cuentas que coincidan", fr: "Aucun compte correspondant", de: "Keine passenden Konten", "pt-BR": "Nenhuma conta correspondente", ru: "Нет подходящих аккаунтов", ar: "لا توجد حسابات مطابقة" },
  Loading: { "zh-CN": "加载中…", "zh-TW": "載入中…", ja: "読み込み中…", ko: "불러오는 중…", es: "Cargando…", fr: "Chargement…", de: "Lädt…", "pt-BR": "Carregando…", ru: "Загрузка…", ar: "جارٍ التحميل…" },
  "Settings saved": { "zh-CN": "设置已保存", "zh-TW": "設定已儲存", ja: "設定を保存しました", ko: "설정 저장됨", es: "Ajustes guardados", fr: "Paramètres enregistrés", de: "Einstellungen gespeichert", "pt-BR": "Configurações salvas", ru: "Настройки сохранены", ar: "تم حفظ الإعدادات" },
  "Auto start enabled": { "zh-CN": "已开启开机自启动", "zh-TW": "已開機自啟動", ja: "自動起動を有効化", ko: "자동 시작 켜짐", es: "Autoarranque activado", fr: "Démarrage auto activé", de: "Autostart aktiviert", "pt-BR": "Início automático ativado", ru: "Автозапуск включен", ar: "تم تمكين التشغيل التلقائي" },
  "Auto start disabled": { "zh-CN": "已关闭开机自启动", "zh-TW": "已關閉開機自啟動", ja: "自動起動を無効化", ko: "자동 시작 꺼짐", es: "Autoarranque desactivado", fr: "Démarrage auto désactivé", de: "Autostart deaktiviert", "pt-BR": "Início automático desativado", ru: "Автозапуск отключен", ar: "تم تعطيل التشغيل التلقائي" },

  // Usage page
  "No data": { "zh-CN": "无数据", "zh-TW": "無資料", ja: "データなし", ko: "데이터 없음", es: "Sin datos", fr: "Aucune donnée", de: "Keine Daten", "pt-BR": "Sem dados", ru: "Нет данных", ar: "لا توجد بيانات" },
  "Token usage trend": { "zh-CN": "Token 使用趋势", "zh-TW": "Token 使用趨勢", ja: "トークン使用推移", ko: "토큰 사용 추이", es: "Tendencia de tokens", fr: "Tendance des jetons", de: "Token-Verlauf", "pt-BR": "Tendência de tokens", ru: "Динамика токенов", ar: "اتجاه استخدام الرموز" },
  "Total requests": { "zh-CN": "总请求数", "zh-TW": "總請求數", ja: "総リクエスト数", ko: "총 요청 수", es: "Solicitudes totales", fr: "Requêtes totales", de: "Anfragen gesamt", "pt-BR": "Solicitações totais", ru: "Всего запросов", ar: "إجمالي الطلبات" },
  "Total tokens": { "zh-CN": "总 Token 数", "zh-TW": "總 Token 數", ja: "総トークン数", ko: "총 토큰 수", es: "Tokens totales", fr: "Jetons totaux", de: "Token gesamt", "pt-BR": "Tokens totais", ru: "Всего токенов", ar: "إجمالي الرموز" },
  "Cached tokens": { "zh-CN": "缓存 Token", "zh-TW": "快取 Token", ja: "キャッシュ済みトークン", ko: "캐시된 토큰", es: "Tokens en caché", fr: "Jetons en cache", de: "Zwischengespeicherte Token", "pt-BR": "Tokens em cache", ru: "Кэшированные токены", ar: "الرموز المخزنة" },
  "Average latency": { "zh-CN": "平均延迟", "zh-TW": "平均延遲", ja: "平均レイテンシ", ko: "평균 지연 시간", es: "Latencia media", fr: "Latence moyenne", de: "Durchschnittliche Latenz", "pt-BR": "Latência média", ru: "Средняя задержка", ar: "متوسط الاستجابة" },
  Today: { "zh-CN": "今日", "zh-TW": "今日", ja: "今日", ko: "오늘", es: "Hoy", fr: "Aujourd'hui", de: "Heute", "pt-BR": "Hoje", ru: "Сегодня", ar: "اليوم" },
  Input: { "zh-CN": "输入", "zh-TW": "輸入", ja: "入力", ko: "입력", es: "Entrada", fr: "Entrée", de: "Eingabe", "pt-BR": "Entrada", ru: "Ввод", ar: "الإدخال" },
  Output: { "zh-CN": "输出", "zh-TW": "輸出", ja: "出力", ko: "출력", es: "Salida", fr: "Sortie", de: "Ausgabe", "pt-BR": "Saída", ru: "Вывод", ar: "الإخراج" },
  "Model distribution": { "zh-CN": "模型分布", "zh-TW": "模型分佈", ja: "モデル分布", ko: "모델 분포", es: "Distribución por modelo", fr: "Répartition par modèle", de: "Modellverteilung", "pt-BR": "Distribuição por modelo", ru: "Распределение по моделям", ar: "توزيع النماذج" },
  "Endpoint distribution": { "zh-CN": "端点分布", "zh-TW": "端點分佈", ja: "エンドポイント分布", ko: "엔드포인트 분포", es: "Distribución por endpoint", fr: "Répartition par endpoint", de: "Endpunkt-Verteilung", "pt-BR": "Distribuição por endpoint", ru: "Распределение по эндпоинтам", ar: "توزيع نقاط النهاية" },
  "API key usage": { "zh-CN": "API 密钥用量", "zh-TW": "API 金鑰用量", ja: "APIキー使用量", ko: "API 키 사용량", es: "Uso por clave API", fr: "Utilisation par clé API", de: "API-Schlüssel-Nutzung", "pt-BR": "Uso por chave de API", ru: "Использование ключей API", ar: "استخدام مفاتيح API" },
  "Request details": { "zh-CN": "请求详情", "zh-TW": "請求詳情", ja: "リクエスト詳細", ko: "요청 상세", es: "Detalles de solicitudes", fr: "Détails des requêtes", de: "Anfragedetails", "pt-BR": "Detalhes das solicitações", ru: "Детали запросов", ar: "تفاصيل الطلبات" },
  Page: { "zh-CN": "页", "zh-TW": "頁", ja: "ページ", ko: "페이지", es: "Página", fr: "Page", de: "Seite", "pt-BR": "Página", ru: "Страница", ar: "صفحة" },
  total: { "zh-CN": "总计", "zh-TW": "總計", ja: "合計", ko: "합계", es: "total", fr: "total", de: "gesamt", "pt-BR": "total", ru: "всего", ar: "الإجمالي" },
  Previous: { "zh-CN": "上一页", "zh-TW": "上一頁", ja: "前へ", ko: "이전", es: "Anterior", fr: "Précédent", de: "Zurück", "pt-BR": "Anterior", ru: "Назад", ar: "السابق" },
  Next: { "zh-CN": "下一页", "zh-TW": "下一頁", ja: "次へ", ko: "다음", es: "Siguiente", fr: "Suivant", de: "Weiter", "pt-BR": "Próximo", ru: "Далее", ar: "التالي" },

  // API Keys page
  "Key created": { "zh-CN": "密钥已创建", "zh-TW": "金鑰已建立", ja: "キーを作成しました", ko: "키 생성됨", es: "Clave creada", fr: "Clé créée", de: "Schlüssel erstellt", "pt-BR": "Chave criada", ru: "Ключ создан", ar: "تم إنشاء المفتاح" },
  Copied: { "zh-CN": "已复制", "zh-TW": "已複製", ja: "コピーしました", ko: "복사됨", es: "Copiado", fr: "Copié", de: "Kopiert", "pt-BR": "Copiado", ru: "Скопировано", ar: "تم النسخ" },
  "Copy failed": { "zh-CN": "复制失败", "zh-TW": "複製失敗", ja: "コピー失敗", ko: "복사 실패", es: "Error al copiar", fr: "Échec de la copie", de: "Kopieren fehlgeschlagen", "pt-BR": "Falha ao copiar", ru: "Ошибка копирования", ar: "فشل النسخ" },
  Copy: { "zh-CN": "复制", "zh-TW": "複製", ja: "コピー", ko: "복사", es: "Copiar", fr: "Copier", de: "Kopieren", "pt-BR": "Copiar", ru: "Копировать", ar: "نسخ" },
  Done: { "zh-CN": "完成", "zh-TW": "完成", ja: "完了", ko: "완료", es: "Hecho", fr: "Terminé", de: "Fertig", "pt-BR": "Concluído", ru: "Готово", ar: "تم" },
  Name: { "zh-CN": "名称", "zh-TW": "名稱", ja: "名前", ko: "이름", es: "Nombre", fr: "Nom", de: "Name", "pt-BR": "Nome", ru: "Имя", ar: "الاسم" },
  "Create key": { "zh-CN": "创建密钥", "zh-TW": "建立金鑰", ja: "キーを作成", ko: "키 생성", es: "Crear clave", fr: "Créer une clé", de: "Schlüssel erstellen", "pt-BR": "Criar chave", ru: "Создать ключ", ar: "إنشاء مفتاح" },
  "Copy this key now — it will not be shown again.": { "zh-CN": "请立即复制此密钥，关闭后不再显示。", "zh-TW": "請立即複製此金鑰，關閉後不再顯示。", ja: "このキーを今すぐコピーしてください。再度表示されません。", ko: "이 키를 지금 복사하세요. 다시 표시되지 않습니다.", es: "Copia esta clave ahora; no se mostrará de nuevo.", fr: "Copiez cette clé maintenant ; elle ne sera plus affichée.", de: "Kopieren Sie diesen Schlüssel jetzt – er wird nicht erneut angezeigt.", "pt-BR": "Copie esta chave agora; ela não será exibida novamente.", ru: "Скопируйте ключ сейчас — он больше не будет показан.", ar: "انسخ هذا المفتاح الآن — لن يظهر مرة أخرى." },
  "API keys": { "zh-CN": "API 密钥", "zh-TW": "API 金鑰", ja: "APIキー", ko: "API 키", es: "Claves API", fr: "Clés API", de: "API-Schlüssel", "pt-BR": "Chaves de API", ru: "API-ключи", ar: "مفاتيح API" },
  Active: { "zh-CN": "启用中", "zh-TW": "啟用中", ja: "有効", ko: "활성", es: "Activa", fr: "Actif", de: "Aktiv", "pt-BR": "Ativa", ru: "Активен", ar: "نشط" },
  Edit: { "zh-CN": "编辑", "zh-TW": "編輯", ja: "編集", ko: "편집", es: "Editar", fr: "Modifier", de: "Bearbeiten", "pt-BR": "Editar", ru: "Изменить", ar: "تعديل" },
  Enable: { "zh-CN": "启用", "zh-TW": "啟用", ja: "有効化", ko: "활성화", es: "Activar", fr: "Activer", de: "Aktivieren", "pt-BR": "Ativar", ru: "Включить", ar: "تمكين" },
  Disable: { "zh-CN": "停用", "zh-TW": "停用", ja: "無効化", ko: "비활성화", es: "Desactivar", fr: "Désactiver", de: "Deaktivieren", "pt-BR": "Desativar", ru: "Отключить", ar: "تعطيل" },
  Save: { "zh-CN": "保存", "zh-TW": "儲存", ja: "保存", ko: "저장", es: "Guardar", fr: "Enregistrer", de: "Speichern", "pt-BR": "Salvar", ru: "Сохранить", ar: "حفظ" },
  Cancel: { "zh-CN": "取消", "zh-TW": "取消", ja: "キャンセル", ko: "취소", es: "Cancelar", fr: "Annuler", de: "Abbrechen", "pt-BR": "Cancelar", ru: "Отмена", ar: "إلغاء" },
  Deleted: { "zh-CN": "已删除", "zh-TW": "已刪除", ja: "削除しました", ko: "삭제됨", es: "Eliminado", fr: "Supprimé", de: "Gelöscht", "pt-BR": "Excluído", ru: "Удалено", ar: "تم الحذف" },

  // Conversations page
  Untitled: { "zh-CN": "未命名", "zh-TW": "未命名", ja: "無題", ko: "제목 없음", es: "Sin título", fr: "Sans titre", de: "Ohne Titel", "pt-BR": "Sem título", ru: "Без названия", ar: "بلا عنوان" },
  "Clean up all": { "zh-CN": "全部清理", "zh-TW": "全部清理", ja: "すべて削除", ko: "모두 정리", es: "Limpiar todo", fr: "Tout nettoyer", de: "Alle bereinigen", "pt-BR": "Limpar tudo", ru: "Очистить все", ar: "تنظيف الكل" },
  Cleaned: { "zh-CN": "已清理", "zh-TW": "已清理", ja: "削除しました", ko: "정리됨", es: "Limpiado", fr: "Nettoyé", de: "Bereinigt", "pt-BR": "Limpo", ru: "Очищено", ar: "تم التنظيف" },
  View: { "zh-CN": "查看", "zh-TW": "檢視", ja: "表示", ko: "보기", es: "Ver", fr: "Voir", de: "Ansehen", "pt-BR": "Ver", ru: "Просмотр", ar: "عرض" },
  "Clean up old conversations on the server?": { "zh-CN": "清理服务器上的旧对话？", "zh-TW": "清理伺服器上的舊對話？", ja: "サーバー上の古い会話を削除しますか？", ko: "서버의 오래된 대화를 정리할까요?", es: "¿Limpiar conversaciones antiguas en el servidor?", fr: "Nettoyer les anciennes conversations sur le serveur ?", de: "Alte Unterhaltungen auf dem Server bereinigen?", "pt-BR": "Limpar conversas antigas no servidor?", ru: "Очистить старые диалоги на сервере?", ar: "تنظيف المحادثات القديمة على الخادم؟" },

  // Proxy Pool page
  "Enter at least one proxy URL": { "zh-CN": "请输入至少一个代理地址", "zh-TW": "請輸入至少一個代理位址", ja: "プロキシURLを1つ以上入力してください", ko: "프록시 URL을 하나 이상 입력하세요", es: "Introduce al menos una URL de proxy", fr: "Saisissez au moins une URL de proxy", de: "Mindestens eine Proxy-URL eingeben", "pt-BR": "Informe ao menos uma URL de proxy", ru: "Введите хотя бы один URL прокси", ar: "أدخل عنوان وكيل واحدًا على الأقل" },
  Added: { "zh-CN": "已添加", "zh-TW": "已新增", ja: "追加しました", ko: "추가됨", es: "Añadido", fr: "Ajouté", de: "Hinzugefügt", "pt-BR": "Adicionado", ru: "Добавлено", ar: "تمت الإضافة" },
  "Connectivity checked": { "zh-CN": "连通性已检测", "zh-TW": "連線性已檢測", ja: "接続を確認しました", ko: "연결 확인됨", es: "Conectividad verificada", fr: "Connectivité vérifiée", de: "Konnektivität geprüft", "pt-BR": "Conectividade verificada", ru: "Подключение проверено", ar: "تم فحص الاتصال" },
  "Add proxies": { "zh-CN": "添加代理", "zh-TW": "新增代理", ja: "プロキシを追加", ko: "프록시 추가", es: "Añadir proxies", fr: "Ajouter des proxys", de: "Proxys hinzufügen", "pt-BR": "Adicionar proxies", ru: "Добавить прокси", ar: "إضافة وكلاء" },
  "One proxy per line. http/https/socks5 supported.": { "zh-CN": "每行一个代理，支持 http/https/socks5。", "zh-TW": "每行一個代理，支援 http/https/socks5。", ja: "1行に1つのプロキシ。http/https/socks5対応。", ko: "한 줄에 하나의 프록시. http/https/socks5 지원.", es: "Un proxy por línea. Compatible con http/https/socks5.", fr: "Un proxy par ligne. http/https/socks5 pris en charge.", de: "Ein Proxy pro Zeile. http/https/socks5 unterstützt.", "pt-BR": "Um proxy por linha. Suporta http/https/socks5.", ru: "По одному прокси в строке. Поддержка http/https/socks5.", ar: "وكيل واحد لكل سطر. يدعم http/https/socks5." },
  "Test connectivity": { "zh-CN": "测试连通性", "zh-TW": "測試連線性", ja: "接続テスト", ko: "연결 테스트", es: "Probar conectividad", fr: "Tester la connectivité", de: "Konnektivität testen", "pt-BR": "Testar conectividade", ru: "Проверить подключение", ar: "اختبار الاتصال" },
  "Check all": { "zh-CN": "全部检测", "zh-TW": "全部檢測", ja: "すべて確認", ko: "모두 확인", es: "Comprobar todo", fr: "Tout vérifier", de: "Alle prüfen", "pt-BR": "Verificar tudo", ru: "Проверить все", ar: "فحص الكل" },
  Healthy: { "zh-CN": "健康", "zh-TW": "健康", ja: "正常", ko: "정상", es: "Saludable", fr: "Sain", de: "Fehlerfrei", "pt-BR": "Saudável", ru: "Работает", ar: "سليم" },
  Unreachable: { "zh-CN": "不可达", "zh-TW": "無法連線", ja: "到達不可", ko: "연결 불가", es: "Inaccesible", fr: "Injoignable", de: "Nicht erreichbar", "pt-BR": "Inacessível", ru: "Недоступен", ar: "غير قابل للوصول" },
  "Upstream error": { "zh-CN": "上游错误", "zh-TW": "上游錯誤", ja: "上流エラー", ko: "업스트림 오류", es: "Error upstream", fr: "Erreur upstream", de: "Upstream-Fehler", "pt-BR": "Erro upstream", ru: "Ошибка upstream", ar: "خطأ المنبع" },
  "Not checked": { "zh-CN": "未检测", "zh-TW": "未檢測", ja: "未確認", ko: "미확인", es: "Sin comprobar", fr: "Non vérifié", de: "Nicht geprüft", "pt-BR": "Não verificado", ru: "Не проверено", ar: "لم يتم الفحص" },
  "Cooling down": { "zh-CN": "冷却中", "zh-TW": "冷卻中", ja: "クールダウン中", ko: "쿨다운 중", es: "Enfriando", fr: "En attente", de: "Abklingzeit", "pt-BR": "Resfriando", ru: "Ожидание", ar: "تهدئة" },

  // Model Test page
  "Test all": { "zh-CN": "全部测试", "zh-TW": "全部測試", ja: "すべてテスト", ko: "모두 테스트", es: "Probar todo", fr: "Tout tester", de: "Alle testen", "pt-BR": "Testar tudo", ru: "Тестировать все", ar: "اختبار الكل" },
  Failed: { "zh-CN": "失败", "zh-TW": "失敗", ja: "失敗", ko: "실패", es: "Fallido", fr: "Échec", de: "Fehlgeschlagen", "pt-BR": "Falhou", ru: "Ошибка", ar: "فشل" },
  "Testing…": { "zh-CN": "测试中…", "zh-TW": "測試中…", ja: "テスト中…", ko: "테스트 중…", es: "Probando…", fr: "Test en cours…", de: "Teste…", "pt-BR": "Testando…", ru: "Тестирование…", ar: "جارٍ الاختبار…" },
  "Not tested": { "zh-CN": "未测试", "zh-TW": "未測試", ja: "未テスト", ko: "미테스트", es: "Sin probar", fr: "Non testé", de: "Nicht getestet", "pt-BR": "Não testado", ru: "Не тестировано", ar: "لم يُختبر" },
  "Test selected": { "zh-CN": "测试所选", "zh-TW": "測試所選", ja: "選択をテスト", ko: "선택 항목 테스트", es: "Probar seleccionados", fr: "Tester la sélection", de: "Auswahl testen", "pt-BR": "Testar selecionados", ru: "Тестировать выбранные", ar: "اختبار المحدد" },
  "Select at least one model": { "zh-CN": "请至少选择一个模型", "zh-TW": "請至少選擇一個模型", ja: "モデルを1つ以上選択してください", ko: "모델을 하나 이상 선택하세요", es: "Selecciona al menos un modelo", fr: "Sélectionnez au moins un modèle", de: "Mindestens ein Modell auswählen", "pt-BR": "Selecione ao menos um modelo", ru: "Выберите хотя бы одну модель", ar: "اختر نموذجًا واحدًا على الأقل" },

  // Dashboard
  Navigation: { "zh-CN": "导航", "zh-TW": "導覽", ja: "ナビゲーション", ko: "탐색", es: "Navegación", fr: "Navigation", de: "Navigation", "pt-BR": "Navegação", ru: "Навигация", ar: "التنقل" },
  requests: { "zh-CN": "次请求", "zh-TW": "次請求", ja: "リクエスト", ko: "요청", es: "solicitudes", fr: "requêtes", de: "Anfragen", "pt-BR": "solicitações", ru: "запросов", ar: "طلبات" },
  "Last 24 hours": { "zh-CN": "最近 24 小时", "zh-TW": "最近 24 小時", ja: "過去24時間", ko: "최근 24시간", es: "Últimas 24 horas", fr: "Dernières 24 heures", de: "Letzte 24 Stunden", "pt-BR": "Últimas 24 horas", ru: "За 24 часа", ar: "آخر 24 ساعة" },
  "All-time usage": { "zh-CN": "累计用量", "zh-TW": "累計用量", ja: "累計使用量", ko: "전체 사용량", es: "Uso total", fr: "Utilisation totale", de: "Gesamtnutzung", "pt-BR": "Uso total", ru: "Общее использование", ar: "الاستخدام الإجمالي" },
  "Request count": { "zh-CN": "请求数", "zh-TW": "請求數", ja: "リクエスト数", ko: "요청 수", es: "Número de solicitudes", fr: "Nombre de requêtes", de: "Anzahl Anfragen", "pt-BR": "Contagem de solicitações", ru: "Количество запросов", ar: "عدد الطلبات" },
  "Cache hits": { "zh-CN": "缓存命中", "zh-TW": "快取命中", ja: "キャッシュヒット", ko: "캐시 적중", es: "Aciertos de caché", fr: "Hits de cache", de: "Cache-Treffer", "pt-BR": "Acertos de cache", ru: "Попадания в кэш", ar: "إصابات الذاكرة" },
  "Hit rate": { "zh-CN": "命中率", "zh-TW": "命中率", ja: "ヒット率", ko: "적중률", es: "Tasa de aciertos", fr: "Taux de réussite", de: "Trefferquote", "pt-BR": "Taxa de acertos", ru: "Доля попаданий", ar: "معدل الإصابة" },
  "Total sessions": { "zh-CN": "总会话数", "zh-TW": "總會話數", ja: "総セッション数", ko: "총 세션 수", es: "Sesiones totales", fr: "Sessions totales", de: "Sitzungen gesamt", "pt-BR": "Sessões totais", ru: "Всего сессий", ar: "إجمالي الجلسات" },
  "Tokens saved": { "zh-CN": "节省 Token", "zh-TW": "節省 Token", ja: "節約トークン", ko: "절약된 토큰", es: "Tokens ahorrados", fr: "Jetons économisés", de: "Gesparte Token", "pt-BR": "Tokens economizados", ru: "Сэкономлено токенов", ar: "الرموز الموفرة" },
  "Quick actions": { "zh-CN": "快捷操作", "zh-TW": "快速操作", ja: "クイック操作", ko: "빠른 작업", es: "Acciones rápidas", fr: "Actions rapides", de: "Schnellaktionen", "pt-BR": "Ações rápidas", ru: "Быстрые действия", ar: "إجراءات سريعة" },
  "Add account": { "zh-CN": "添加账号", "zh-TW": "新增帳號", ja: "アカウント追加", ko: "계정 추가", es: "Añadir cuenta", fr: "Ajouter un compte", de: "Konto hinzufügen", "pt-BR": "Adicionar conta", ru: "Добавить аккаунт", ar: "إضافة حساب" },
  "Create API key": { "zh-CN": "创建 API 密钥", "zh-TW": "建立 API 金鑰", ja: "APIキー作成", ko: "API 키 생성", es: "Crear clave API", fr: "Créer une clé API", de: "API-Schlüssel erstellen", "pt-BR": "Criar chave de API", ru: "Создать API-ключ", ar: "إنشاء مفتاح API" },
  "Clean conversations": { "zh-CN": "清理对话", "zh-TW": "清理對話", ja: "会話を整理", ko: "대화 정리", es: "Limpiar conversaciones", fr: "Nettoyer les conversations", de: "Unterhaltungen bereinigen", "pt-BR": "Limpar conversas", ru: "Очистить диалоги", ar: "تنظيف المحادثات" },
  "Delete conversations you no longer need": { "zh-CN": "删除不再需要的对话", "zh-TW": "刪除不再需要的對話", ja: "不要な会話を削除", ko: "더 이상 필요 없는 대화 삭제", es: "Elimina las conversaciones que ya no necesitas", fr: "Supprimez les conversations inutiles", de: "Nicht mehr benötigte Unterhaltungen löschen", "pt-BR": "Exclua conversas que não precisa mais", ru: "Удалите ненужные диалоги", ar: "احذف المحادثات التي لم تعد بحاجتها" },
  "Recent requests": { "zh-CN": "最近请求", "zh-TW": "最近請求", ja: "最近のリクエスト", ko: "최근 요청", es: "Solicitudes recientes", fr: "Requêtes récentes", de: "Letzte Anfragen", "pt-BR": "Solicitações recentes", ru: "Недавние запросы", ar: "الطلبات الأخيرة" },
  "Open Accounts to add an account": { "zh-CN": "请在「账号」页添加账号", "zh-TW": "請在「帳號」頁新增帳號", ja: "「アカウント」ページで追加してください", ko: "계정 페이지에서 추가하세요", es: "Abre Cuentas para añadir una cuenta", fr: "Ouvrez Comptes pour ajouter un compte", de: "Konten öffnen, um ein Konto hinzuzufügen", "pt-BR": "Abra Contas para adicionar uma conta", ru: "Откройте «Аккаунты», чтобы добавить аккаунт", ar: "افتح الحسابات لإضافة حساب" },
  "Open API Keys to create a key": { "zh-CN": "请在「API 密钥」页创建密钥", "zh-TW": "請在「API 金鑰」頁建立金鑰", ja: "「APIキー」ページで作成してください", ko: "API 키 페이지에서 생성하세요", es: "Abre Claves API para crear una clave", fr: "Ouvrez Clés API pour créer une clé", de: "API-Schlüssel öffnen, um einen Schlüssel zu erstellen", "pt-BR": "Abra Chaves de API para criar uma chave", ru: "Откройте «API-ключи», чтобы создать ключ", ar: "افتح مفاتيح API لإنشاء مفتاح" },
  "Open Conversations to clean up": { "zh-CN": "请在「对话」页清理", "zh-TW": "請在「對話」頁清理", ja: "「会話」ページで整理してください", ko: "대화 페이지에서 정리하세요", es: "Abre Conversaciones para limpiar", fr: "Ouvrez Conversations pour nettoyer", de: "Unterhaltungen öffnen zum Bereinigen", "pt-BR": "Abra Conversas para limpar", ru: "Откройте «Диалоги» для очистки", ar: "افتح المحادثات للتنظيف" },

  // Accounts / PKCE
  "Account management": { "zh-CN": "账号管理", "zh-TW": "帳號管理", ja: "アカウント管理", ko: "계정 관리", es: "Gestión de cuentas", fr: "Gestion des comptes", de: "Kontoverwaltung", "pt-BR": "Gerenciamento de contas", ru: "Управление аккаунтами", ar: "إدارة الحسابات" },
  "Authorize and manage Microsoft accounts": { "zh-CN": "授权并管理 Microsoft 账号", "zh-TW": "授權並管理 Microsoft 帳號", ja: "Microsoft アカウントを認証・管理", ko: "Microsoft 계정 인증 및 관리", es: "Autoriza y gestiona cuentas de Microsoft", fr: "Autorisez et gérez les comptes Microsoft", de: "Microsoft-Konten autorisieren und verwalten", "pt-BR": "Autorize e gerencie contas da Microsoft", ru: "Авторизация и управление аккаунтами Microsoft", ar: "تفويض حسابات Microsoft وإدارتها" },
  "Click Start authorization below": { "zh-CN": "点击下方「开始授权」", "zh-TW": "點擊下方「開始授權」", ja: "下の「認証を開始」をクリック", ko: "아래 '인증 시작'을 클릭하세요", es: "Haz clic en «Iniciar autorización» abajo", fr: "Cliquez sur « Démarrer l'autorisation » ci-dessous", de: "Klicken Sie unten auf „Autorisierung starten“", "pt-BR": "Clique em «Iniciar autorização» abaixo", ru: "Нажмите «Начать авторизацию» ниже", ar: "انقر «بدء التفويض» أدناه" },
  "A popup opens the Microsoft sign-in page.": { "zh-CN": "会弹出一个窗口打开 Microsoft 登录页。", "zh-TW": "會彈出視窗開啟 Microsoft 登入頁。", ja: "ポップアップで Microsoft サインインページが開きます。", ko: "팝업에서 Microsoft 로그인 페이지가 열립니다.", es: "Se abre una ventana con la página de inicio de sesión de Microsoft.", fr: "Une fenêtre s'ouvre sur la page de connexion Microsoft.", de: "Ein Popup öffnet die Microsoft-Anmeldeseite.", "pt-BR": "Um popup abre a página de login da Microsoft.", ru: "Откроется окно со страницей входа Microsoft.", ar: "تفتح نافذة منبثقة صفحة تسجيل دخول Microsoft." },
  "Sign in, then copy the address-bar URL from the popup": { "zh-CN": "登录后复制弹出窗口地址栏里的完整网址", "zh-TW": "登入後複製彈出視窗網址列的完整網址", ja: "サインイン後、ポップアップのアドレスバーのURLをコピー", ko: "로그인 후 팝업 주소 표시줄의 URL을 복사하세요", es: "Inicia sesión y copia la URL de la barra de direcciones de la ventana", fr: "Connectez-vous puis copiez l'URL de la barre d'adresse", de: "Anmelden und die URL aus der Adressleiste kopieren", "pt-BR": "Faça login e copie a URL da barra de endereços", ru: "Войдите и скопируйте URL из адресной строки", ar: "سجّل الدخول ثم انسخ عنوان URL من شريط العنوان" },
  "The popup ends on a blank or error page — that is normal. Copy the whole URL, including the code and state parameters.": { "zh-CN": "弹出页最后会停在空白或报错页——这是正常的。请复制整条 URL（含 code 和 state 参数）。", "zh-TW": "彈出頁最後會停在空白或錯誤頁——這是正常的。請複製整條 URL（含 code 與 state 參數）。", ja: "最後に空白またはエラーページになりますが正常です。code と state を含むURL全体をコピーしてください。", ko: "마지막에 빈 페이지나 오류 페이지가 나오는 것이 정상입니다. code와 state를 포함한 전체 URL을 복사하세요.", es: "La ventana termina en una página en blanco o de error; es normal. Copia la URL completa, incluidos los parámetros code y state.", fr: "La fenêtre finit sur une page blanche ou d'erreur — c'est normal. Copiez l'URL complète, avec les paramètres code et state.", de: "Das Popup endet auf einer leeren oder Fehlerseite – das ist normal. Kopieren Sie die vollständige URL inklusive code und state.", "pt-BR": "A janela termina numa página em branco ou de erro — é normal. Copie a URL completa, incluindo os parâmetros code e state.", ru: "Окно завершится пустой страницей или ошибкой — это нормально. Скопируйте URL целиком, включая code и state.", ar: "تنتهي النافذة بصفحة فارغة أو خطأ — هذا طبيعي. انسخ عنوان URL كاملًا مع معاملَي code و state." },
  "Paste that long URL and confirm": { "zh-CN": "粘贴这条长 URL 并确认", "zh-TW": "貼上這條長 URL 並確認", ja: "その長いURLを貼り付けて確認", ko: "그 긴 URL을 붙여넣고 확인하세요", es: "Pega esa URL larga y confirma", fr: "Collez cette longue URL et confirmez", de: "Diese lange URL einfügen und bestätigen", "pt-BR": "Cole essa URL longa e confirme", ru: "Вставьте этот длинный URL и подтвердите", ar: "الصق هذا العنوان الطويل وأكّد" },
  "Paste it into the callback field and click Confirm and add.": { "zh-CN": "粘贴到下面的「回调地址」输入框，点击「确认添加」。", "zh-TW": "貼到下方「回呼網址」輸入框，點擊「確認新增」。", ja: "下のコールバック欄に貼り付け「確認して追加」をクリック。", ko: "아래 콜백 입력란에 붙여넣고 '확인 후 추가'를 클릭하세요.", es: "Pégalo en el campo de callback y pulsa «Confirmar y añadir».", fr: "Collez-le dans le champ callback et cliquez sur « Confirmer et ajouter ».", de: "In das Callback-Feld einfügen und „Bestätigen und hinzufügen“ klicken.", "pt-BR": "Cole no campo de callback e clique em «Confirmar e adicionar».", ru: "Вставьте в поле callback и нажмите «Подтвердить и добавить».", ar: "الصقه في حقل الاستدعاء وانقر «تأكيد وإضافة»." },
  "Start authorization": { "zh-CN": "开始授权", "zh-TW": "開始授權", ja: "認証を開始", ko: "인증 시작", es: "Iniciar autorización", fr: "Démarrer l'autorisation", de: "Autorisierung starten", "pt-BR": "Iniciar autorização", ru: "Начать авторизацию", ar: "بدء التفويض" },
  "Callback URL": { "zh-CN": "回调地址", "zh-TW": "回呼網址", ja: "コールバックURL", ko: "콜백 URL", es: "URL de callback", fr: "URL de callback", de: "Callback-URL", "pt-BR": "URL de callback", ru: "Callback URL", ar: "عنوان URL للاستدعاء" },
  "Paste the full callback URL…": { "zh-CN": "粘贴完整的回调地址…", "zh-TW": "貼上完整的回呼網址…", ja: "コールバックURL全体を貼り付け…", ko: "전체 콜백 URL을 붙여넣으세요…", es: "Pega la URL de callback completa…", fr: "Collez l'URL de callback complète…", de: "Vollständige Callback-URL einfügen…", "pt-BR": "Cole a URL de callback completa…", ru: "Вставьте полный callback URL…", ar: "الصق عنوان URL الكامل…" },
  "Confirm and add": { "zh-CN": "确认添加", "zh-TW": "確認新增", ja: "確認して追加", ko: "확인 후 추가", es: "Confirmar y añadir", fr: "Confirmer et ajouter", de: "Bestätigen und hinzufügen", "pt-BR": "Confirmar e adicionar", ru: "Подтвердить и добавить", ar: "تأكيد وإضافة" },
  "Popup blocked — allow popups and try again": { "zh-CN": "弹窗被拦截——请允许弹窗后重试", "zh-TW": "彈窗被封鎖——請允許彈窗後重試", ja: "ポップアップがブロックされました。許可して再試行してください", ko: "팝업이 차단되었습니다. 허용 후 다시 시도하세요", es: "Ventana bloqueada: permite las ventanas emergentes y reintenta", fr: "Popup bloqué — autorisez les popups et réessayez", de: "Popup blockiert – Popups erlauben und erneut versuchen", "pt-BR": "Popup bloqueado — permita popups e tente de novo", ru: "Всплывающее окно заблокировано — разрешите их и повторите", ar: "تم حظر النافذة المنبثقة — اسمح بها وأعد المحاولة" },
  "Sign in the popup, then paste the full callback URL below": { "zh-CN": "在弹窗中登录，然后把完整的回调地址粘贴到下方", "zh-TW": "在彈窗中登入，然後把完整的回呼網址貼到下方", ja: "ポップアップでサインインし、コールバックURL全体を下に貼り付けてください", ko: "팝업에서 로그인한 뒤 전체 콜백 URL을 아래에 붙여넣으세요", es: "Inicia sesión en la ventana y pega abajo la URL de callback completa", fr: "Connectez-vous dans la fenêtre puis collez l'URL complète ci-dessous", de: "Im Popup anmelden und die vollständige Callback-URL unten einfügen", "pt-BR": "Faça login no popup e cole a URL de callback completa abaixo", ru: "Войдите в окне и вставьте полный callback URL ниже", ar: "سجّل الدخول في النافذة والصق عنوان الاستدعاء الكامل أدناه" },
  "Paste the callback URL": { "zh-CN": "请粘贴回调地址", "zh-TW": "請貼上回呼網址", ja: "コールバックURLを貼り付けてください", ko: "콜백 URL을 붙여넣으세요", es: "Pega la URL de callback", fr: "Collez l'URL de callback", de: "Callback-URL einfügen", "pt-BR": "Cole a URL de callback", ru: "Вставьте callback URL", ar: "الصق عنوان الاستدعاء" },
  "Authorization succeeded": { "zh-CN": "授权成功", "zh-TW": "授權成功", ja: "認証に成功しました", ko: "인증 성공", es: "Autorización correcta", fr: "Autorisation réussie", de: "Autorisierung erfolgreich", "pt-BR": "Autorização concluída", ru: "Авторизация успешна", ar: "تم التفويض بنجاح" },
  All: { "zh-CN": "全部", "zh-TW": "全部", ja: "すべて", ko: "전체", es: "Todas", fr: "Toutes", de: "Alle", "pt-BR": "Todas", ru: "Все", ar: "الكل" },
  selected: { "zh-CN": "已选", "zh-TW": "已選", ja: "選択中", ko: "선택됨", es: "seleccionadas", fr: "sélectionné(s)", de: "ausgewählt", "pt-BR": "selecionadas", ru: "выбрано", ar: "محدد" },
  "Select at least one account": { "zh-CN": "请至少选择一个账号", "zh-TW": "請至少選擇一個帳號", ja: "アカウントを1つ以上選択してください", ko: "계정을 하나 이상 선택하세요", es: "Selecciona al menos una cuenta", fr: "Sélectionnez au moins un compte", de: "Mindestens ein Konto auswählen", "pt-BR": "Selecione ao menos uma conta", ru: "Выберите хотя бы один аккаунт", ar: "اختر حسابًا واحدًا على الأقل" },
  "Delete this account?": { "zh-CN": "删除该账号？", "zh-TW": "刪除該帳號？", ja: "このアカウントを削除しますか？", ko: "이 계정을 삭제할까요?", es: "¿Eliminar esta cuenta?", fr: "Supprimer ce compte ?", de: "Dieses Konto löschen?", "pt-BR": "Excluir esta conta?", ru: "Удалить этот аккаунт?", ar: "حذف هذا الحساب؟" },
  "Enter a system prompt first": { "zh-CN": "请先输入系统提示词", "zh-TW": "請先輸入系統提示詞", ja: "先にシステムプロンプトを入力してください", ko: "먼저 시스템 프롬프트를 입력하세요", es: "Introduce primero un prompt del sistema", fr: "Saisissez d'abord un prompt système", de: "Zuerst einen Systemprompt eingeben", "pt-BR": "Informe primeiro um prompt do sistema", ru: "Сначала введите системный промпт", ar: "أدخل مطالبة النظام أولًا" },

  // API keys / model test
  "Manage access keys": { "zh-CN": "管理访问密钥", "zh-TW": "管理存取金鑰", ja: "アクセスキーの管理", ko: "액세스 키 관리", es: "Gestiona las claves de acceso", fr: "Gérer les clés d'accès", de: "Zugriffsschlüssel verwalten", "pt-BR": "Gerenciar chaves de acesso", ru: "Управление ключами доступа", ar: "إدارة مفاتيح الوصول" },
  "Key list": { "zh-CN": "密钥列表", "zh-TW": "金鑰列表", ja: "キー一覧", ko: "키 목록", es: "Lista de claves", fr: "Liste des clés", de: "Schlüsselliste", "pt-BR": "Lista de chaves", ru: "Список ключей", ar: "قائمة المفاتيح" },
  "The full key is shown only once after creation": { "zh-CN": "完整密钥仅在创建时显示一次", "zh-TW": "完整金鑰僅在建立時顯示一次", ja: "完全なキーは作成時に一度だけ表示されます", ko: "전체 키는 생성 시 한 번만 표시됩니다", es: "La clave completa solo se muestra una vez tras crearla", fr: "La clé complète n'est affichée qu'une fois à la création", de: "Der vollständige Schlüssel wird nur einmal angezeigt", "pt-BR": "A chave completa é exibida apenas uma vez", ru: "Полный ключ показывается только один раз", ar: "يظهر المفتاح الكامل مرة واحدة فقط" },
  "Test models": { "zh-CN": "测试模型", "zh-TW": "測試模型", ja: "モデルをテスト", ko: "모델 테스트", es: "Probar modelos", fr: "Tester les modèles", de: "Modelle testen", "pt-BR": "Testar modelos", ru: "Тестировать модели", ar: "اختبار النماذج" },
  "Send a minimal request to verify a model": { "zh-CN": "发送一个最小请求以验证模型可用", "zh-TW": "發送一個最小請求以驗證模型可用", ja: "最小リクエストを送信してモデルを確認", ko: "최소 요청을 보내 모델을 확인합니다", es: "Envía una solicitud mínima para verificar el modelo", fr: "Envoyez une requête minimale pour vérifier le modèle", de: "Eine minimale Anfrage senden, um das Modell zu prüfen", "pt-BR": "Envie uma solicitação mínima para verificar o modelo", ru: "Отправьте минимальный запрос для проверки модели", ar: "أرسل طلبًا بسيطًا للتحقق من النموذج" },
  Prompt: { "zh-CN": "提示词", "zh-TW": "提示詞", ja: "プロンプト", ko: "프롬프트", es: "Prompt", fr: "Prompt", de: "Prompt", "pt-BR": "Prompt", ru: "Промпт", ar: "المطالبة" },
  Test: { "zh-CN": "测试", "zh-TW": "測試", ja: "テスト", ko: "테스트", es: "Probar", fr: "Tester", de: "Testen", "pt-BR": "Testar", ru: "Тест", ar: "اختبار" },
  Close: { "zh-CN": "关闭", "zh-TW": "關閉", ja: "閉じる", ko: "닫기", es: "Cerrar", fr: "Fermer", de: "Schließen", "pt-BR": "Fechar", ru: "Закрыть", ar: "إغلاق" },

  // Settings fields
  "Chat timeout (5-3600s)": { "zh-CN": "聊天超时（5-3600 秒）", "zh-TW": "聊天逾時（5-3600 秒）", ja: "チャットタイムアウト（5-3600秒）", ko: "채팅 타임아웃(5-3600초)", es: "Tiempo de espera de chat (5-3600 s)", fr: "Délai de chat (5-3600 s)", de: "Chat-Timeout (5-3600 s)", "pt-BR": "Tempo limite do chat (5-3600 s)", ru: "Тайм-аут чата (5-3600 с)", ar: "مهلة الدردشة (5-3600 ث)" },
  "Image timeout (5-3600s)": { "zh-CN": "生图超时（5-3600 秒）", "zh-TW": "生圖逾時（5-3600 秒）", ja: "画像タイムアウト（5-3600秒）", ko: "이미지 타임아웃(5-3600초)", es: "Tiempo de espera de imagen (5-3600 s)", fr: "Délai d'image (5-3600 s)", de: "Bild-Timeout (5-3600 s)", "pt-BR": "Tempo limite de imagem (5-3600 s)", ru: "Тайм-аут изображения (5-3600 с)", ar: "مهلة الصورة (5-3600 ث)" },
  "Context window": { "zh-CN": "上下文窗口", "zh-TW": "上下文視窗", ja: "コンテキストウィンドウ", ko: "컨텍스트 창", es: "Ventana de contexto", fr: "Fenêtre de contexte", de: "Kontextfenster", "pt-BR": "Janela de contexto", ru: "Окно контекста", ar: "نافذة السياق" },
  "Max output tokens": { "zh-CN": "最大输出 Token", "zh-TW": "最大輸出 Token", ja: "最大出力トークン", ko: "최대 출력 토큰", es: "Tokens de salida máx.", fr: "Jetons de sortie max.", de: "Max. Ausgabe-Token", "pt-BR": "Tokens de saída máx.", ru: "Макс. выходных токенов", ar: "أقصى رموز الإخراج" },
  "Max tool calls/turn (1-64)": { "zh-CN": "每轮最大工具调用（1-64）", "zh-TW": "每輪最大工具呼叫（1-64）", ja: "1ターン最大ツール呼び出し（1-64）", ko: "턴당 최대 도구 호출(1-64)", es: "Máx. llamadas por turno (1-64)", fr: "Appels d'outils max./tour (1-64)", de: "Max. Tool-Aufrufe/Runde (1-64)", "pt-BR": "Máx. chamadas por turno (1-64)", ru: "Макс. вызовов инструментов/ход (1-64)", ar: "أقصى استدعاءات أدوات/دور (1-64)" },
  "Max tool rounds (1-512)": { "zh-CN": "最大工具轮次（1-512）", "zh-TW": "最大工具輪次（1-512）", ja: "最大ツールラウンド（1-512）", ko: "최대 도구 라운드(1-512)", es: "Rondas de herramientas máx. (1-512)", fr: "Tours d'outils max. (1-512)", de: "Max. Tool-Runden (1-512)", "pt-BR": "Rodadas de ferramentas máx. (1-512)", ru: "Макс. раундов инструментов (1-512)", ar: "أقصى جولات أدوات (1-512)" },
  "Max conversation messages": { "zh-CN": "最大对话消息数", "zh-TW": "最大對話訊息數", ja: "最大会話メッセージ数", ko: "최대 대화 메시지 수", es: "Mensajes de conversación máx.", fr: "Messages de conversation max.", de: "Max. Unterhaltungsnachrichten", "pt-BR": "Mensagens de conversa máx.", ru: "Макс. сообщений в диалоге", ar: "أقصى رسائل المحادثة" },
  "Account concurrency (1-64)": { "zh-CN": "账号并发（1-64）", "zh-TW": "帳號並行（1-64）", ja: "アカウント同時実行（1-64）", ko: "계정 동시성(1-64)", es: "Concurrencia por cuenta (1-64)", fr: "Concurrence par compte (1-64)", de: "Konto-Parallelität (1-64)", "pt-BR": "Concorrência por conta (1-64)", ru: "Параллелизм аккаунта (1-64)", ar: "تزامن الحساب (1-64)" },
  "Rate limit cooldown (5-3600s)": { "zh-CN": "限流冷却（5-3600 秒）", "zh-TW": "限流冷卻（5-3600 秒）", ja: "レート制限クールダウン（5-3600秒）", ko: "속도 제한 쿨다운(5-3600초)", es: "Enfriamiento por límite (5-3600 s)", fr: "Attente après limite (5-3600 s)", de: "Rate-Limit-Abklingzeit (5-3600 s)", "pt-BR": "Resfriamento por limite (5-3600 s)", ru: "Ожидание после лимита (5-3600 с)", ar: "تهدئة حد المعدل (5-3600 ث)" },
  "Transient throttle cooldown (5-600s)": { "zh-CN": "瞬时节流冷却（5-600 秒）", "zh-TW": "瞬時節流冷卻（5-600 秒）", ja: "一時的スロットル冷却（5-600秒）", ko: "일시적 제한 쿨다운(5-600초)", es: "Enfriamiento por limitación transitoria (5-600 s)", fr: "Attente après limitation transitoire (5-600 s)", de: "Abklingzeit bei temporärer Drosselung (5-600 s)", "pt-BR": "Resfriamento por limitação transitória (5-600 s)", ru: "Ожидание при временном лимите (5-600 с)", ar: "تهدئة الخنق المؤقت (5-600 ث)" },
  Scenario: { "zh-CN": "场景", "zh-TW": "場景", ja: "シナリオ", ko: "시나리오", es: "Escenario", fr: "Scénario", de: "Szenario", "pt-BR": "Cenário", ru: "Сценарий", ar: "السيناريو" },
  License: { "zh-CN": "许可类型", "zh-TW": "授權類型", ja: "ライセンス", ko: "라이선스", es: "Licencia", fr: "Licence", de: "Lizenz", "pt-BR": "Licença", ru: "Лицензия", ar: "الترخيص" },
  "Log level": { "zh-CN": "日志级别", "zh-TW": "日誌級別", ja: "ログレベル", ko: "로그 수준", es: "Nivel de registro", fr: "Niveau de journal", de: "Log-Level", "pt-BR": "Nível de log", ru: "Уровень журнала", ar: "مستوى السجل" },
  "Sign in via Microsoft OAuth": { "zh-CN": "通过 Microsoft OAuth 登录", "zh-TW": "透過 Microsoft OAuth 登入", ja: "Microsoft OAuthでサインイン", ko: "Microsoft OAuth로 로그인", es: "Iniciar sesión con Microsoft OAuth", fr: "Se connecter via Microsoft OAuth", de: "Über Microsoft OAuth anmelden", "pt-BR": "Entrar com Microsoft OAuth", ru: "Войти через Microsoft OAuth", ar: "تسجيل الدخول عبر Microsoft OAuth" },
  "Generate a key for API access": { "zh-CN": "生成用于 API 访问的密钥", "zh-TW": "產生用於 API 存取的金鑰", ja: "APIアクセス用のキーを生成", ko: "API 액세스용 키 생성", es: "Genera una clave para acceso API", fr: "Générez une clé pour l'accès API", de: "Schlüssel für API-Zugriff erstellen", "pt-BR": "Gere uma chave para acesso à API", ru: "Создайте ключ для доступа к API", ar: "أنشئ مفتاحًا للوصول إلى API" },
  "Search accounts…": { "zh-CN": "搜索账号…", "zh-TW": "搜尋帳號…", ja: "アカウントを検索…", ko: "계정 검색…", es: "Buscar cuentas…", fr: "Rechercher des comptes…", de: "Konten suchen…", "pt-BR": "Buscar contas…", ru: "Поиск аккаунтов…", ar: "ابحث عن الحسابات…" },
  "Enter public model id": { "zh-CN": "输入公开模型 ID", "zh-TW": "輸入公開模型 ID", ja: "公開モデルIDを入力", ko: "공개 모델 ID 입력", es: "Introduce el ID del modelo público", fr: "Saisissez l'ID du modèle public", de: "Öffentliche Modell-ID eingeben", "pt-BR": "Informe o ID do modelo público", ru: "Введите ID публичной модели", ar: "أدخل معرّف النموذج العام" },
  "Invalid model id": { "zh-CN": "模型 ID 无效", "zh-TW": "模型 ID 無效", ja: "無効なモデルID", ko: "잘못된 모델 ID", es: "ID de modelo no válido", fr: "ID de modèle invalide", de: "Ungültige Modell-ID", "pt-BR": "ID de modelo inválido", ru: "Недопустимый ID модели", ar: "معرّف النموذج غير صالح" },
  "Mapping already exists": { "zh-CN": "映射已存在", "zh-TW": "映射已存在", ja: "マッピングは既に存在します", ko: "매핑이 이미 존재합니다", es: "El mapeo ya existe", fr: "Le mappage existe déjà", de: "Zuordnung existiert bereits", "pt-BR": "O mapeamento já existe", ru: "Сопоставление уже существует", ar: "التعيين موجود بالفعل" },
  "Clear custom system prompt for selected accounts?": { "zh-CN": "清空所选账号的自定义系统提示词？", "zh-TW": "清空所選帳號的自訂系統提示詞？", ja: "選択したアカウントのカスタムシステムプロンプトを消去しますか？", ko: "선택한 계정의 사용자 지정 시스템 프롬프트를 지우시겠습니까?", es: "¿Borrar el prompt de sistema personalizado de las cuentas seleccionadas?", fr: "Effacer l'invite système personnalisée des comptes sélectionnés ?", de: "Benutzerdefinierten Systemprompt der ausgewählten Konten löschen?", "pt-BR": "Limpar o prompt de sistema personalizado das contas selecionadas?", ru: "Очистить пользовательский системный промпт выбранных аккаунтов?", ar: "مسح مطالبة النظام المخصصة للحسابات المحددة؟" },
  hits: { "zh-CN": "次命中", "zh-TW": "次命中", ja: "ヒット", ko: "히트", es: "aciertos", fr: "succès", de: "Treffer", "pt-BR": "acertos", ru: "попаданий", ar: "إصابات" },
  saved: { "zh-CN": "已节省", "zh-TW": "已節省", ja: "節約", ko: "절약", es: "ahorrados", fr: "économisés", de: "gespart", "pt-BR": "economizados", ru: "сэкономлено", ar: "موفَّرة" },
  "Memory V2": { "zh-CN": "记忆 V2", "zh-TW": "記憶 V2", ja: "メモリ V2", ko: "메모리 V2", es: "Memoria V2", fr: "Mémoire V2", de: "Speicher V2", "pt-BR": "Memória V2", ru: "Память V2", ar: "الذاكرة V2" },
  "Deep Work": { "zh-CN": "深度工作", "zh-TW": "深度工作", ja: "ディープワーク", ko: "딥 워크", es: "Trabajo profundo", fr: "Travail profond", de: "Deep Work", "pt-BR": "Trabalho profundo", ru: "Глубокая работа", ar: "العمل العميق" },
  "Computer Use": { "zh-CN": "计算机操作", "zh-TW": "電腦操作", ja: "コンピュータ操作", ko: "컴퓨터 사용", es: "Uso del equipo", fr: "Utilisation de l'ordinateur", de: "Computernutzung", "pt-BR": "Uso do computador", ru: "Использование компьютера", ar: "استخدام الحاسوب" },
  "Realtime Voice": { "zh-CN": "实时语音", "zh-TW": "即時語音", ja: "リアルタイム音声", ko: "실시간 음성", es: "Voz en tiempo real", fr: "Voix en temps réel", de: "Echtzeit-Sprache", "pt-BR": "Voz em tempo real", ru: "Голос в реальном времени", ar: "الصوت الفوري" },
  "System Prompt Override": { "zh-CN": "系统提示词覆盖", "zh-TW": "系統提示詞覆寫", ja: "システムプロンプト上書き", ko: "시스템 프롬프트 재정의", es: "Anular prompt del sistema", fr: "Remplacer le prompt système", de: "Systemprompt überschreiben", "pt-BR": "Substituir prompt do sistema", ru: "Переопределение системного промпта", ar: "تجاوز مطالبة النظام" },
  "Image API": { "zh-CN": "图像 API", "zh-TW": "圖像 API", ja: "画像API", ko: "이미지 API", es: "API de imágenes", fr: "API d'images", de: "Bild-API", "pt-BR": "API de imagens", ru: "API изображений", ar: "واجهة الصور" },
  "Designer Image 4o": { "zh-CN": "Designer 图像 4o", "zh-TW": "Designer 圖像 4o", ja: "Designer画像4o", ko: "Designer 이미지 4o", es: "Imagen Designer 4o", fr: "Image Designer 4o", de: "Designer-Bild 4o", "pt-BR": "Imagem Designer 4o", ru: "Изображение Designer 4o", ar: "صورة Designer 4o" },
  "Code Canvas": { "zh-CN": "代码画布", "zh-TW": "程式碼畫布", ja: "コードキャンバス", ko: "코드 캔버스", es: "Lienzo de código", fr: "Canvas de code", de: "Code-Canvas", "pt-BR": "Canvas de código", ru: "Код-канвас", ar: "لوحة الكود" },
  "Sydney Reconnect": { "zh-CN": "Sydney 重连", "zh-TW": "Sydney 重連", ja: "Sydney再接続", ko: "Sydney 재연결", es: "Reconexión Sydney", fr: "Reconnexion Sydney", de: "Sydney-Wiederverbindung", "pt-BR": "Reconexão Sydney", ru: "Переподключение Sydney", ar: "إعادة اتصال Sydney" },

  // Login & shared table headers
  "Administrator Login": { "zh-CN": "管理员登录", "zh-TW": "管理員登入", ja: "管理者ログイン", ko: "관리자 로그인", es: "Inicio de sesión de administrador", fr: "Connexion administrateur", de: "Administrator-Anmeldung", "pt-BR": "Login de administrador", ru: "Вход администратора", ar: "تسجيل دخول المسؤول" },
  Password: { "zh-CN": "密码", "zh-TW": "密碼", ja: "パスワード", ko: "비밀번호", es: "Contraseña", fr: "Mot de passe", de: "Passwort", "pt-BR": "Senha", ru: "Пароль", ar: "كلمة المرور" },
  "Remember me for 30 days": { "zh-CN": "记住我 30 天", "zh-TW": "記住我 30 天", ja: "30日間ログイン状態を保持", ko: "30일간 로그인 유지", es: "Recordarme 30 días", fr: "Se souvenir de moi 30 jours", de: "30 Tage angemeldet bleiben", "pt-BR": "Lembrar-me por 30 dias", ru: "Запомнить меня на 30 дней", ar: "تذكرني لمدة 30 يومًا" },
  "Sign in": { "zh-CN": "登录", "zh-TW": "登入", ja: "ログイン", ko: "로그인", es: "Iniciar sesión", fr: "Se connecter", de: "Anmelden", "pt-BR": "Entrar", ru: "Войти", ar: "تسجيل الدخول" },
  "Login successful": { "zh-CN": "登录成功", "zh-TW": "登入成功", ja: "ログインしました", ko: "로그인 성공", es: "Sesión iniciada", fr: "Connexion réussie", de: "Anmeldung erfolgreich", "pt-BR": "Login bem-sucedido", ru: "Вход выполнен", ar: "تم تسجيل الدخول" },
  "Too many failed attempts, try again later": { "zh-CN": "登录失败次数过多，请稍后重试", "zh-TW": "登入失敗次數過多，請稍後重試", ja: "ログイン失敗が多すぎます。しばらくして再試行してください。", ko: "로그인 실패가 너무 많습니다. 잠시 후 다시 시도하세요.", es: "Demasiados intentos fallidos; inténtalo más tarde.", fr: "Trop de tentatives échouées ; réessayez plus tard.", de: "Zu viele Fehlversuche; später erneut versuchen.", "pt-BR": "Muitas tentativas falhas; tente mais tarde.", ru: "Слишком много неудачных попыток; повторите позже.", ar: "محاولات فاشلة كثيرة؛ حاول لاحقًا." },
  Account: { "zh-CN": "账号", "zh-TW": "帳號", ja: "アカウント", ko: "계정", es: "Cuenta", fr: "Compte", de: "Konto", "pt-BR": "Conta", ru: "Аккаунт", ar: "الحساب" },
  Calls: { "zh-CN": "调用次数", "zh-TW": "呼叫次數", ja: "呼び出し回数", ko: "호출 수", es: "Llamadas", fr: "Appels", de: "Aufrufe", "pt-BR": "Chamadas", ru: "Вызовы", ar: "الاستدعاءات" },
  Status: { "zh-CN": "状态", "zh-TW": "狀態", ja: "ステータス", ko: "상태", es: "Estado", fr: "État", de: "Status", "pt-BR": "Status", ru: "Статус", ar: "الحالة" },
  Scheduling: { "zh-CN": "调度", "zh-TW": "調度", ja: "スケジュール", ko: "스케줄", es: "Programación", fr: "Planification", de: "Planung", "pt-BR": "Agendamento", ru: "Планирование", ar: "الجدولة" },
  Actions: { "zh-CN": "操作", "zh-TW": "操作", ja: "操作", ko: "작업", es: "Acciones", fr: "Actions", de: "Aktionen", "pt-BR": "Ações", ru: "Действия", ar: "الإجراءات" },
  Updated: { "zh-CN": "更新时间", "zh-TW": "更新時間", ja: "更新日時", ko: "업데이트됨", es: "Actualizado", fr: "Mis à jour", de: "Aktualisiert", "pt-BR": "Atualizado", ru: "Обновлено", ar: "آخر تحديث" },

  // Table headers & misc (new pages)
  Time: { "zh-CN": "时间", "zh-TW": "時間", ja: "時刻", ko: "시간", es: "Hora", fr: "Heure", de: "Zeit", "pt-BR": "Hora", ru: "Время", ar: "الوقت" },
  Key: { "zh-CN": "密钥", "zh-TW": "金鑰", ja: "キー", ko: "키", es: "Clave", fr: "Clé", de: "Schlüssel", "pt-BR": "Chave", ru: "Ключ", ar: "المفتاح" },
  Model: { "zh-CN": "模型", "zh-TW": "模型", ja: "モデル", ko: "모델", es: "Modelo", fr: "Modèle", de: "Modell", "pt-BR": "Modelo", ru: "Модель", ar: "النموذج" },
  Endpoint: { "zh-CN": "端点", "zh-TW": "端點", ja: "エンドポイント", ko: "엔드포인트", es: "Endpoint", fr: "Endpoint", de: "Endpunkt", "pt-BR": "Endpoint", ru: "Эндпоинт", ar: "نقطة النهاية" },
  Tokens: { "zh-CN": "Token", "zh-TW": "Token", ja: "トークン", ko: "토큰", es: "Tokens", fr: "Jetons", de: "Token", "pt-BR": "Tokens", ru: "Токены", ar: "الرموز" },
  Latency: { "zh-CN": "延迟", "zh-TW": "延遲", ja: "レイテンシ", ko: "지연 시간", es: "Latencia", fr: "Latence", de: "Latenz", "pt-BR": "Latência", ru: "Задержка", ar: "الاستجابة" },
  Prefix: { "zh-CN": "前缀", "zh-TW": "前綴", ja: "プレフィックス", ko: "접두사", es: "Prefijo", fr: "Préfixe", de: "Präfix", "pt-BR": "Prefixo", ru: "Префикс", ar: "البادئة" },
  Created: { "zh-CN": "创建时间", "zh-TW": "建立時間", ja: "作成日時", ko: "생성일", es: "Creado", fr: "Créé", de: "Erstellt", "pt-BR": "Criado", ru: "Создан", ar: "تاريخ الإنشاء" },
  "Last used": { "zh-CN": "最后使用", "zh-TW": "最後使用", ja: "最終使用", ko: "마지막 사용", es: "Último uso", fr: "Dernière utilisation", de: "Zuletzt verwendet", "pt-BR": "Último uso", ru: "Последнее использование", ar: "آخر استخدام" },
  Messages: { "zh-CN": "消息数", "zh-TW": "訊息數", ja: "メッセージ数", ko: "메시지 수", es: "Mensajes", fr: "Messages", de: "Nachrichten", "pt-BR": "Mensagens", ru: "Сообщения", ar: "الرسائل" },
  "Last updated": { "zh-CN": "最后更新", "zh-TW": "最後更新", ja: "最終更新", ko: "마지막 업데이트", es: "Última actualización", fr: "Dernière mise à jour", de: "Zuletzt aktualisiert", "pt-BR": "Última atualização", ru: "Последнее обновление", ar: "آخر تحديث" },
  URL: { "zh-CN": "地址", "zh-TW": "位址", ja: "URL", ko: "URL", es: "URL", fr: "URL", de: "URL", "pt-BR": "URL", ru: "URL", ar: "الرابط" },
  Failures: { "zh-CN": "失败次数", "zh-TW": "失敗次數", ja: "失敗回数", ko: "실패 횟수", es: "Fallos", fr: "Échecs", de: "Fehler", "pt-BR": "Falhas", ru: "Сбои", ar: "الإخفاقات" },
  "Cooldown until": { "zh-CN": "冷却至", "zh-TW": "冷卻至", ja: "クールダウン期限", ko: "쿨다운 종료", es: "Enfriando hasta", fr: "En attente jusqu'à", de: "Abklingzeit bis", "pt-BR": "Resfriando até", ru: "Ожидание до", ar: "تهدئة حتى" },
  Reply: { "zh-CN": "回复", "zh-TW": "回覆", ja: "応答", ko: "응답", es: "Respuesta", fr: "Réponse", de: "Antwort", "pt-BR": "Resposta", ru: "Ответ", ar: "الرد" },
  "24h": { "zh-CN": "24 小时", "zh-TW": "24 小時", ja: "24時間", ko: "24시간", es: "24 h", fr: "24 h", de: "24 Std.", "pt-BR": "24 h", ru: "24 ч", ar: "24 ساعة" },
  days: { "zh-CN": "天", "zh-TW": "天", ja: "日", ko: "일", es: "días", fr: "jours", de: "Tage", "pt-BR": "dias", ru: "дн.", ar: "أيام" },
  "Key renamed": { "zh-CN": "密钥已重命名", "zh-TW": "金鑰已重新命名", ja: "キー名を変更しました", ko: "키 이름 변경됨", es: "Clave renombrada", fr: "Clé renommée", de: "Schlüssel umbenannt", "pt-BR": "Chave renomeada", ru: "Ключ переименован", ar: "تمت إعادة تسمية المفتاح" },
};

let current: Locale = "zh-CN";

export function setLocale(l: Locale) {
  current = l;
  try {
    localStorage.setItem("m365_locale", l);
  } catch {}
}

export function getLocale(): Locale {
  return current;
}

export function detectLocale(): Locale {
  try {
    const saved = localStorage.getItem("m365_locale") as Locale | null;
    if (saved && LOCALES.some((l) => l.id === saved)) return saved;
  } catch {}
  const nav = navigator.language;
  if (LOCALES.some((l) => l.id === nav)) return nav as Locale;
  if (nav.startsWith("zh")) return nav.includes("TW") || nav.includes("HK") ? "zh-TW" : "zh-CN";
  if (nav.startsWith("pt")) return "pt-BR";
  return "en";
}

export function t(key: string): string {
  if (current === "en") return key;
  const entry = dict[key];
  if (!entry) return key;
  return entry[current] ?? entry["zh-CN"] ?? key;
}
