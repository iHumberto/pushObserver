/**
 * pushObserver i18n — lightweight bilingual support (PT-BR / EN-US).
 * Zero dependencies. Reads/writes localStorage('lang').
 * Exposes: t(key), setLang(lang), getLang(), initI18N()
 */
const translations = {
    'pt-BR': {
        title: 'pushObserver',
        nav_hooks: 'Hooks',
        nav_new: '+ Novo',
        dashboard_title: 'Hooks',
        table_id: 'ID',
        table_repo: 'Repositório',
        table_branch: 'Branch',
        table_services: 'Serviços',
        table_status: 'Status',
        table_actions: 'Ações',
        status_never: 'nunca',
        btn_edit: 'Editar',
        empty_hooks: 'Nenhum hook configurado.',
        empty_hooks_link: 'Crie seu primeiro hook',
        form_create_title: 'Criar Hook',
        form_edit_title: 'Editar',
        label_hook_id: 'Hook ID *',
        hint_hook_id: 'Identificador único. Usado na URL do webhook: /hook/{id}',
        placeholder_hook_id: 'ex: homelab',
        label_branch: 'Branch',
        hint_branch: 'Branch Git a monitorar. Padrão: main',
        placeholder_branch: 'main',
        label_repo_url: 'URL do Repositório *',
        hint_repo_url: 'URL SSH ou HTTPS. Para repos privados, configure git_ssh_key abaixo.',
        placeholder_repo_url: 'git@github.com:usuario/repo.git',
        label_repo_dir: 'Diretório do Repositório *',
        hint_repo_dir: 'Caminho absoluto onde o repositório será clonado.',
        placeholder_repo_dir: '/home/webhook/repos/meu-app',
        label_ssh_key: 'Caminho da Chave SSH',
        hint_ssh_key: 'Deploy key para repositórios privados. Deixe vazio para HTTPS ou repos públicos.',
        placeholder_ssh_key: '/home/webhook/.ssh/deploy_key',
        label_hmac_type: 'Tipo HMAC',
        hmac_sha256: 'SHA-256 (GitHub, Forgejo, Gitea)',
        hmac_sha1: 'SHA-1 (GitHub legado)',
        hmac_plain: 'Token (GitLab)',
        label_hmac_secret: 'Segredo HMAC',
        hint_hmac_secret: 'Use sintaxe ${ENV_VAR} para evitar secrets em texto puro.',
        placeholder_hmac_secret: '${WEBHOOK_SECRET}',
        label_hmac_header: 'Header HMAC',
        hint_hmac_header: 'Header HTTP contendo a assinatura HMAC.',
        placeholder_hmac_header: 'X-Hub-Signature-256',
        label_content_type: 'Content-Type',
        hint_content_type: 'Content-Type esperado nos webhooks recebidos.',
        ct_json: 'JSON',
        ct_form: 'Form (URL-encoded)',
        btn_save: 'Salvar Alterações',
        btn_create: 'Criar Hook',
        btn_cancel: 'Cancelar',
        back_to_hooks: '← Voltar para todos os hooks',
        detail_repo: 'Repo:',
        detail_branch: 'Branch:',
        detail_dir: 'Dir:',
        detail_services: 'Serviços',
        svc_service: 'Serviço',
        svc_path: 'Caminho',
        svc_trigger: 'Gatilho de Restart',
        svc_status: 'Status',
        empty_services: 'Nenhum serviço detectado.',
        scan_link: 'Escanear repo por serviços →',
        btn_scan: '🔍 Buscar novos serviços',
        btn_deploy: '▶ Deploy Agora',
        btn_edit_hook: '✏ Editar Hook',
        btn_delete: '🗑 Excluir',
        confirm_delete: 'Excluir hook',
        hook_count: 'Hooks ({count})',
        services_count: 'Serviços ({count})',
        notifications_title: 'Notificações',
        label_notifications_hook: 'Notificações',
        notify_on_success: 'No Sucesso',
        notify_on_failure: 'Na Falha',
        notify_on_no_changes: 'Sem Mudanças',
        label_apprise_url: 'Apprise URL',
        hint_apprise_url: 'URL do container Apprise. Ex: http://apprise:8000',
        label_tag_success: 'Tag — Sucesso',
        label_tag_failure: 'Tag — Falha',
        label_tag_no_changes: 'Tag — Sem Mudanças',
        placeholder_tag_no_changes: 'deploy-no-changes (opcional)',
        hint_tag_no_changes: 'Deixe vazio para não notificar quando não houver mudanças.',
        btn_save_notifications: 'Salvar Notificações'
    },
    'en-US': {
        title: 'pushObserver',
        nav_hooks: 'Hooks',
        nav_new: '+ New',
        dashboard_title: 'Hooks',
        table_id: 'ID',
        table_repo: 'Repository',
        table_branch: 'Branch',
        table_services: 'Services',
        table_status: 'Status',
        table_actions: 'Actions',
        status_never: 'never',
        btn_edit: 'Edit',
        empty_hooks: 'No hooks configured.',
        empty_hooks_link: 'Create your first hook',
        form_create_title: 'Create Hook',
        form_edit_title: 'Edit',
        label_hook_id: 'Hook ID *',
        hint_hook_id: 'Unique identifier. Used in webhook URL: /hook/{id}',
        placeholder_hook_id: 'e.g. homelab',
        label_branch: 'Branch',
        hint_branch: 'Git branch to track. Default: main',
        placeholder_branch: 'main',
        label_repo_url: 'Repository URL *',
        hint_repo_url: 'SSH or HTTPS URL. For private repos, configure git_ssh_key below.',
        placeholder_repo_url: 'git@github.com:user/repo.git',
        label_repo_dir: 'Repository Directory *',
        hint_repo_dir: 'Absolute path where the repository will be cloned.',
        placeholder_repo_dir: '/home/webhook/repos/my-app',
        label_ssh_key: 'SSH Key Path',
        hint_ssh_key: 'Deploy key for private repositories. Leave empty for public repos or HTTPS.',
        placeholder_ssh_key: '/home/webhook/.ssh/deploy_key',
        label_hmac_type: 'HMAC Type',
        hmac_sha256: 'SHA-256 (GitHub, Forgejo, Gitea)',
        hmac_sha1: 'SHA-1 (GitHub legacy)',
        hmac_plain: 'Token (GitLab)',
        label_hmac_secret: 'HMAC Secret',
        hint_hmac_secret: 'Use ${ENV_VAR} syntax to avoid plain-text secrets.',
        placeholder_hmac_secret: '${WEBHOOK_SECRET}',
        label_hmac_header: 'HMAC Header',
        hint_hmac_header: 'HTTP header containing the HMAC signature.',
        placeholder_hmac_header: 'X-Hub-Signature-256',
        label_content_type: 'Content-Type',
        hint_content_type: 'Expected Content-Type of incoming webhooks.',
        ct_json: 'JSON',
        ct_form: 'Form (URL-encoded)',
        btn_save: 'Save Changes',
        btn_create: 'Create Hook',
        btn_cancel: 'Cancel',
        back_to_hooks: '← Back to all hooks',
        detail_repo: 'Repo:',
        detail_branch: 'Branch:',
        detail_dir: 'Dir:',
        detail_services: 'Services',
        svc_service: 'Service',
        svc_path: 'Path',
        svc_trigger: 'Restart Trigger',
        svc_status: 'Status',
        empty_services: 'No services detected.',
        scan_link: 'Scan repo for services →',
        btn_scan: '🔍 Scan for new services',
        btn_deploy: '▶ Deploy Now',
        btn_edit_hook: '✏ Edit Hook',
        btn_delete: '🗑 Delete',
        confirm_delete: 'Delete hook',
        hook_count: 'Hooks ({count})',
        services_count: 'Services ({count})',
        notifications_title: 'Notifications',
        label_notifications_hook: 'Notifications',
        notify_on_success: 'On Success',
        notify_on_failure: 'On Failure',
        notify_on_no_changes: 'On No Changes',
        label_apprise_url: 'Apprise URL',
        hint_apprise_url: 'Apprise container URL. e.g. http://apprise:8000',
        label_tag_success: 'Tag — Success',
        label_tag_failure: 'Tag — Failure',
        label_tag_no_changes: 'Tag — No Changes',
        placeholder_tag_no_changes: 'deploy-no-changes (optional)',
        hint_tag_no_changes: 'Leave empty to skip notification when nothing changed.',
        btn_save_notifications: 'Save Notification Settings'
    }
};

var _currentLang = (function () {
    try {
        var stored = localStorage.getItem('lang');
        if (stored && translations[stored]) return stored;
    } catch (e) {}
    return 'pt-BR';
})();

function t(key) {
    var lang = translations[_currentLang];
    if (lang && lang[key] !== undefined) return lang[key];
    var fallback = translations['pt-BR'];
    return (fallback && fallback[key] !== undefined) ? fallback[key] : key;
}

function getLang() { return _currentLang; }

function setLang(lang) {
    if (!translations[lang]) return;
    _currentLang = lang;
    try { localStorage.setItem('lang', lang); } catch (e) {}
    document.documentElement.lang = lang;
    applyTranslations();
    updateLangSelector();
}

function applyTranslations() {
    var els = document.querySelectorAll('[data-i18n]');
    for (var i = 0; i < els.length; i++) {
        var key = els[i].getAttribute('data-i18n');
        if (key) els[i].textContent = t(key);
    }
    els = document.querySelectorAll('[data-i18n-placeholder]');
    for (var i = 0; i < els.length; i++) {
        var key = els[i].getAttribute('data-i18n-placeholder');
        if (key) els[i].placeholder = t(key);
    }
    els = document.querySelectorAll('[data-i18n-href]');
    for (var i = 0; i < els.length; i++) {
        var parts = els[i].getAttribute('data-i18n-href').split('||');
        var href = '';
        for (var j = 0; j < parts.length; j++) {
            href += (j % 2 === 0) ? parts[j] : t(parts[j]);
        }
        els[i].href = href;
    }
    els = document.querySelectorAll('[data-i18n-onsubmit]');
    for (var i = 0; i < els.length; i++) {
        var parts = els[i].getAttribute('data-i18n-onsubmit').split('||');
        var msg = '';
        for (var j = 0; j < parts.length; j++) {
            msg += (j % 2 === 0) ? parts[j] : t(parts[j]);
        }
        els[i].setAttribute('onsubmit', msg);
    }
}

function confirmDelete(hookId) {
    return confirm(t('confirm_delete') + ' ' + hookId + '?');
}

function buildLangSelector() {
    var bar = document.getElementById('lang-bar');
    if (!bar) return;
    bar.innerHTML = '';
    var flags = { 'pt-BR': '\uD83C\uDDE7\uD83C\uDDF7', 'en-US': '\uD83C\uDDFA\uD83C\uDDF8' };
    var select = document.createElement('select');
    select.id = 'lang-select';
    select.setAttribute('aria-label', 'Idioma / Language');
    var langs = ['pt-BR', 'en-US'];
    for (var i = 0; i < langs.length; i++) {
        var code = langs[i];
        var opt = document.createElement('option');
        opt.value = code;
        opt.textContent = flags[code] + ' ' + code;
        select.appendChild(opt);
    }
    select.value = _currentLang;
    select.addEventListener('change', function () { setLang(select.value); });
    bar.appendChild(select);
}

function updateLangSelector() {
    var sel = document.getElementById('lang-select');
    if (sel) sel.value = _currentLang;
}

function initI18N() {
    buildLangSelector();
    document.documentElement.lang = _currentLang;
    applyTranslations();
}

// Auto-initialize on DOMContentLoaded (no inline onload needed).
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initI18N);
} else {
    initI18N();
}
