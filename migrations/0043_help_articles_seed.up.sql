-- Seed data for help_articles (Part 2 of the onboarding/help/UX batch):
-- ten articles covering the features most new firms touch first.
-- Hardcoded as migration INSERTs rather than a runtime seeding path -
-- this is reference documentation content, versioned and reviewed the
-- same way any other schema change is, not per-tenant configuration data
-- (see internal/helparticles's own doc comment for the read-only
-- rationale this seeding strategy follows). Content is Markdown
-- (rendered client-side by the help panel - see
-- web/src/components/Help/HelpPanel.tsx).

INSERT INTO help_articles (slug, title_en, title_tr, content_en, content_tr, related_route) VALUES
(
    'workflow-engine-basics',
    'Understanding the workflow engine',
    'İş akışı motorunu anlamak',
    E'# Understanding the workflow engine\n\nZonaryOS''s workflow engine lets you model any process - sales, purchasing, approvals - as a set of **states** and the **transitions** that move an instance between them.\n\n1. **Define a workflow** with its states and allowed transitions.\n2. **Create an instance** of that workflow (e.g. one sales order).\n3. Team members with the right permission trigger the transitions that move it forward.\n\nEach rule can run automatically or require human approval - your call, per rule, at any time.',
    E'# İş akışı motorunu anlamak\n\nZonaryOS''un iş akışı motoru; satış, satın alma, onaylar gibi her süreci **durumlar** ve bir örneği bu durumlar arasında taşıyan **geçişler** olarak modellemenizi sağlar.\n\n1. Durumları ve izin verilen geçişleriyle bir **iş akışı tanımlayın**.\n2. Bu iş akışının bir **örneğini oluşturun** (örneğin bir satış siparişi).\n3. Doğru izne sahip ekip üyeleri, süreci ilerleten geçişleri tetikler.\n\nHer kural otomatik çalışabilir veya insan onayı gerektirebilir - bu tercihi kural bazında, istediğiniz zaman siz yaparsınız.',
    '/workflows'
),
(
    'inventory-getting-started',
    'Setting up your product catalog',
    'Ürün kataloğunuzu oluşturma',
    E'# Setting up your product catalog\n\nAdd each product with a SKU, name, unit, and optional pricing. Stock is tracked per location, and every change is recorded as an immutable movement - so you always have a full history of how a quantity got where it is.\n\nSet a **minimum quantity** on a product to have it flagged as low stock once on-hand quantity drops below that threshold.',
    E'# Ürün kataloğunuzu oluşturma\n\nHer ürünü bir SKU, ad, birim ve isteğe bağlı fiyatlandırmayla ekleyin. Stok konuma göre takip edilir ve her değişiklik değiştirilemez bir hareket olarak kaydedilir - böylece bir miktarın nasıl oluştuğuna dair her zaman tam bir geçmişiniz olur.\n\nEldeki miktar bu eşiğin altına düştüğünde düşük stok olarak işaretlenmesi için bir ürüne **minimum miktar** belirleyin.',
    '/inventory'
),
(
    'invoicing-basics',
    'Creating and sending invoices',
    'Fatura oluşturma ve gönderme',
    E'# Creating and sending invoices\n\nAn invoice is built from line items, each with a quantity, unit price, and tax rate. Once issued, record payments against it to track what''s outstanding - a partially paid invoice shows its remaining balance automatically.\n\nInvoices integrate with your chart of accounts: issuing one posts the matching journal entries for you.',
    E'# Fatura oluşturma ve gönderme\n\nBir fatura; her biri miktar, birim fiyat ve vergi oranına sahip kalemlerden oluşturulur. Düzenlendikten sonra, kalan bakiyeyi takip etmek için ödemeleri faturaya işleyin - kısmen ödenmiş bir fatura kalan bakiyesini otomatik olarak gösterir.\n\nFaturalar hesap planınızla entegredir: bir fatura düzenlemek, ilgili muhasebe kayıtlarını sizin için otomatik olarak oluşturur.',
    '/invoices'
),
(
    'hr-people-and-contracts',
    'Managing people and contracts',
    'Kişi ve sözleşme yönetimi',
    E'# Managing people and contracts\n\nEvery employee, contractor, or partner is a **person** record, which can hold one or more **contracts** over time (a renewal or a role change doesn''t overwrite history - it adds a new contract).\n\nDeactivating a person keeps their history intact while removing them from active rosters and assignment pickers.',
    E'# Kişi ve sözleşme yönetimi\n\nHer çalışan, yüklenici veya iş ortağı bir **kişi** kaydıdır ve zaman içinde bir veya daha fazla **sözleşmeye** sahip olabilir (bir yenileme veya rol değişikliği geçmişin üzerine yazmaz - yeni bir sözleşme ekler).\n\nBir kişiyi pasifleştirmek, geçmişini korurken onu aktif listelerden ve atama seçicilerinden kaldırır.',
    '/hr'
),
(
    'reports-and-dashboards',
    'Building and running reports',
    'Rapor oluşturma ve çalıştırma',
    E'# Building and running reports\n\nA report definition describes what data to pull (the entity, grouping, and metrics) without you writing any SQL. Save a definition once and re-run it anytime - each run is recorded so you can compare results over time.\n\nScheduled reports can run automatically and notify the right people when they finish.',
    E'# Rapor oluşturma ve çalıştırma\n\nBir rapor tanımı, herhangi bir SQL yazmadan hangi verinin (varlık, gruplama ve metrikler) çekileceğini tarif eder. Bir tanımı bir kez kaydedin ve istediğiniz zaman yeniden çalıştırın - her çalıştırma kaydedilir, böylece sonuçları zaman içinde karşılaştırabilirsiniz.\n\nZamanlanmış raporlar otomatik olarak çalışabilir ve tamamlandığında doğru kişileri bilgilendirebilir.',
    '/reports'
),
(
    'crm-customers-and-opportunities',
    'Tracking customers and opportunities',
    'Müşteri ve fırsat takibi',
    E'# Tracking customers and opportunities\n\nCustomers hold contact details and a full activity timeline. Opportunities track a potential deal through its own stages, independent of your workflow engine''s process states, so your sales pipeline and your operational processes can evolve separately.',
    E'# Müşteri ve fırsat takibi\n\nMüşteriler; iletişim bilgilerini ve tam bir etkinlik zaman çizelgesini tutar. Fırsatlar, iş akışı motorunuzun süreç durumlarından bağımsız olarak kendi aşamaları boyunca potansiyel bir anlaşmayı takip eder; böylece satış huniniz ve operasyonel süreçleriniz ayrı ayrı gelişebilir.',
    '/crm'
),
(
    'contracts-and-renewals',
    'Managing contracts and renewal dates',
    'Sözleşme ve yenileme tarihi yönetimi',
    E'# Managing contracts and renewal dates\n\nStore vendor, customer, and partner contracts in one place with their key dates - start, end, and renewal - so nothing lapses unnoticed. A contract nearing its renewal date is easy to spot from the list view.',
    E'# Sözleşme ve yenileme tarihi yönetimi\n\nTedarikçi, müşteri ve iş ortağı sözleşmelerini başlangıç, bitiş ve yenileme gibi önemli tarihleriyle birlikte tek bir yerde saklayın; böylece hiçbir şey fark edilmeden süresi dolmaz. Yenileme tarihine yaklaşan bir sözleşme liste görünümünden kolayca fark edilir.',
    '/contracts'
),
(
    'projects-and-tasks',
    'Organizing projects and tasks',
    'Proje ve görev düzenleme',
    E'# Organizing projects and tasks\n\nA project groups related tasks under one place with its own timeline. Assign tasks to team members, track status, and see a project''s overall progress at a glance.',
    E'# Proje ve görev düzenleme\n\nBir proje, ilgili görevleri kendi zaman çizelgesiyle tek bir yerde gruplandırır. Görevleri ekip üyelerine atayın, durumu takip edin ve bir projenin genel ilerlemesini bir bakışta görün.',
    '/projects'
),
(
    'permissions-and-roles',
    'Understanding permissions and roles',
    'İzin ve rolleri anlamak',
    E'# Understanding permissions and roles\n\nEvery action in ZonaryOS is gated by a permission key. Roles bundle permissions together and are assigned to team members - the owner role always has every permission. Use Permission Audit Mode to see exactly which permission guards each button on a page.',
    E'# İzin ve rolleri anlamak\n\nZonaryOS''taki her işlem bir izin anahtarıyla korunur. Roller izinleri bir araya getirir ve ekip üyelerine atanır - sahip rolü her zaman tüm izinlere sahiptir. Bir sayfadaki her düğmenin tam olarak hangi izinle korunduğunu görmek için İzin Denetim Modu''nu kullanın.',
    '/settings'
),
(
    'webhooks-and-integrations',
    'Setting up webhooks and integrations',
    'Webhook ve entegrasyon kurulumu',
    E'# Setting up webhooks and integrations\n\nA webhook posts a signed payload to your own URL whenever an event you subscribe to happens - a product created, an invoice paid, and more. Use this to connect ZonaryOS to other systems without polling the API.\n\nEach webhook has its own secret, used to verify the payload came from ZonaryOS.',
    E'# Webhook ve entegrasyon kurulumu\n\nBir webhook; abone olduğunuz bir olay gerçekleştiğinde (bir ürünün oluşturulması, bir faturanın ödenmesi ve daha fazlası) kendi URL''nize imzalı bir veri gönderir. Bunu, API''yi sürekli sorgulamadan ZonaryOS''u diğer sistemlere bağlamak için kullanın.\n\nHer webhook''un, verinin gerçekten ZonaryOS''tan geldiğini doğrulamak için kullanılan kendi gizli anahtarı vardır.',
    '/settings'
);
