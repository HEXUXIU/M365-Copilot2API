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
