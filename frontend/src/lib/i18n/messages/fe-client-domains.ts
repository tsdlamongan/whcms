/** FE-CLIENT-DOMAINS i18n namespace - keys under `clientDomains.*`. */
import type { Locale } from '../i18n.svelte';

const messages: Partial<Record<Locale, Record<string, unknown>>> = {
	id: {
		clientDomains: {
			list: {
				title: 'Domain Saya',
				subtitle: 'Kelola domain terdaftar pada akun Anda.',
				searchPlaceholder: 'Cari nama domain…',
				statusLabel: 'Status',
				colDomain: 'Domain',
				colStatus: 'Status',
				colExpiry: 'Kedaluwarsa',
				colAmount: 'Biaya Perpanjangan',
				colAutoRenew: 'Perpanjang Otomatis',
				manage: 'Kelola',
				emptyTitle: 'Belum ada domain',
				emptyDesc: 'Anda belum memiliki domain terdaftar pada akun ini.',
				registerCta: 'Daftarkan Domain'
			},
			detail: {
				back: 'Kembali ke daftar domain',
				notFound: 'Domain tidak ditemukan atau Anda tidak memiliki akses.',
				overviewTitle: 'Informasi Domain',
				renewNow: 'Perpanjang Sekarang',
				renewTitle: 'Perpanjang Domain',
				renewPeriod: 'Periode perpanjangan',
				years: '{n} tahun',
				renewTotal: 'Total perkiraan',
				renewHint:
					'Invoice perpanjangan akan dibuat dan Anda akan diarahkan ke halaman pembayaran.',
				status: 'Status',
				registrationDate: 'Tanggal registrasi',
				expiryDate: 'Tanggal kedaluwarsa',
				nextDueDate: 'Jatuh tempo berikutnya',
				billingCycle: 'Siklus tagihan',
				recurringAmount: 'Biaya perpanjangan',
				autoRenew: 'Perpanjang otomatis',
				idProtection: 'Proteksi ID'
			},
			tabs: {
				overview: 'Ringkasan',
				nameservers: 'Nameserver',
				dns: 'DNS',
				epp: 'Kode EPP',
				contact: 'Kontak',
				addons: 'Tambahan'
			},
			ns: {
				title: 'Nameserver',
				desc: 'Atur nameserver domain Anda. Minimal 2 nameserver wajib diisi.',
				label: 'Nameserver {n}',
				optionalHint: 'Opsional',
				save: 'Simpan Nameserver',
				needTwo: 'Minimal 2 nameserver harus diisi.',
				invalidHost: 'Format nameserver tidak valid (contoh: ns1.contoh.com).',
				saved: 'Nameserver berhasil disimpan.'
			},
			dns: {
				title: 'Pengelola DNS',
				desc: 'Tambah, ubah, atau hapus record DNS di bawah, lalu simpan seluruh perubahan sekaligus.',
				type: 'Tipe',
				host: 'Host',
				value: 'Nilai',
				ttl: 'TTL',
				prio: 'Prioritas',
				add: 'Tambah Record',
				remove: 'Hapus',
				save: 'Simpan Perubahan',
				empty: 'Belum ada record DNS. Tambahkan record pertama Anda.',
				unsaved: 'Ada perubahan yang belum disimpan.',
				invalid: 'Data record DNS tidak valid.',
				invalidRow:
					'Record pada baris {row} tidak valid. Periksa tipe, host, nilai, TTL, dan prioritas.',
				saved: 'Record DNS berhasil disimpan.'
			},
			epp: {
				title: 'Kode EPP / Auth',
				desc: 'Kode EPP diperlukan untuk mentransfer domain ke registrar lain. Jaga kerahasiaan kode ini.',
				reveal: 'Tampilkan Kode EPP',
				codeLabel: 'Kode EPP',
				copy: 'Salin',
				copied: 'Kode EPP disalin ke clipboard.',
				unavailable: 'Kode EPP tidak tersedia saat ini.'
			},
			contact: {
				title: 'Kontak Registran',
				desc: 'Data pemilik (registrant) yang terdaftar untuk domain ini.',
				loadFailed:
					'Data kontak tidak dapat dimuat ({message}). Anda tetap dapat mengirim pembaruan.',
				firstName: 'Nama depan',
				lastName: 'Nama belakang',
				company: 'Perusahaan',
				email: 'Email',
				phone: 'Telepon',
				address: 'Alamat',
				city: 'Kota',
				state: 'Provinsi',
				postcode: 'Kode pos',
				country: 'Negara (kode ISO)',
				required: 'Wajib diisi.',
				invalidEmail: 'Format email tidak valid.',
				invalidCountry: 'Gunakan kode negara 2 huruf, contoh: ID.',
				save: 'Simpan Kontak',
				saved: 'Kontak registran berhasil disimpan.'
			},
			addons: {
				title: 'Tambahan Domain',
				desc: 'Aktifkan tambahan untuk domain ini. Biaya perpanjangan tahunan akan disesuaikan otomatis.',
				save: 'Simpan Tambahan',
				saved: 'Tambahan domain berhasil disimpan.',
				perYear: '/tahun',
				dnsRequiredTitle: 'Fitur DNS Management diperlukan',
				dnsRequiredDesc:
					'Aktifkan tambahan DNS Management pada tab Tambahan untuk mengelola record DNS domain ini.',
				goToAddons: 'Buka tab Tambahan'
			},
			cycle: {
				one_time: 'Sekali bayar',
				monthly: 'Bulanan',
				quarterly: '3 Bulanan',
				semiannually: '6 Bulanan',
				annually: 'Tahunan',
				biennially: '2 Tahunan'
			},
			docs: {
				badge: 'Perlu dokumen',
				badgeEmail: 'Perlu verifikasi',
				title: 'Dokumen persyaratan diperlukan',
				titleEmail: 'Verifikasi email diperlukan',
				intro:
					'Ekstensi {tld} mewajibkan dokumen persyaratan. Domain ini aktif sementara; unggah dokumennya agar tidak ditangguhkan.',
				introEmail:
					'Registran harus mengonfirmasi domain melalui email verifikasi yang dikirim registrar. Domain ditangguhkan bila tidak dikonfirmasi tepat waktu.',
				stateLabel: 'Status verifikasi',
				state: {
					waiting: 'Menunggu dokumen',
					verifying: 'Sedang diverifikasi',
					document_validating: 'Dokumen sedang divalidasi',
					active: 'Terverifikasi'
				},
				emailState: {
					waiting: 'Menunggu verifikasi email',
					verifying: 'Sedang diverifikasi'
				},
				rejectedTitle: 'Dokumen ditolak',
				rejectedWithReason:
					'Dokumen yang Anda unggah ditolak: {reason}. Unggah ulang dokumen yang benar.',
				rejected: 'Dokumen yang Anda unggah ditolak. Unggah ulang dokumen yang benar.',
				suspendedTitle: 'Domain ditangguhkan',
				suspendedWithReason:
					'Domain ditangguhkan: {reason}. Lengkapi verifikasi agar domain aktif kembali.',
				suspended: 'Domain ditangguhkan. Lengkapi verifikasi agar domain aktif kembali.',
				deadlinesTitle: 'Jadwal verifikasi',
				uploadBy: 'Unggah dokumen paling lambat',
				suspendOn: 'Domain ditangguhkan jika belum ada dokumen valid',
				suspendOnEmail: 'Domain ditangguhkan jika email belum diverifikasi',
				deleteOn: 'Domain dihapus tanpa pengembalian dana',
				requirementsTitle: 'Dokumen yang dibutuhkan',
				req: {
					identity: 'KTP elektronik / paspor / e.id / identitas lain yang sah',
					companyLegal:
						'Badan hukum Indonesia: NIB / NPWP / SK Kumham (atas nama perusahaan, mis. PT, CV, Firma)',
					intlBrand:
						'Merek internasional: sertifikat merek internasional / sertifikat PSE (Indonesia) dan surat kuasa kepada subjek hukum Indonesia',
					orgDeed:
						'Organisasi / perkumpulan / yayasan: akta notaris pendirian atau surat permohonan domain dari ketua',
					highEdDecree: 'SK pendirian perguruan tinggi dari kementerian / lembaga yang berwenang',
					requestLetterRector: 'Surat permohonan domain dari rektor atau pimpinan lembaga',
					requestLetterHead: 'Surat permohonan domain dari kepala sekolah / pimpinan lembaga',
					schoolPermit:
						'SK izin operasional sekolah (atau SK pendirian lembaga untuk pendidikan non-formal)',
					requestLetterPesantren:
						'Surat permohonan domain dari pimpinan pondok pesantren / lembaga',
					nameMatchesInstitution:
						'Nama domain harus sesuai dengan nama sekolah / lembaga yang mendaftar'
				},
				upload: 'Unggah Dokumen',
				reupload: 'Unggah Ulang Dokumen',
				linkReadyTitle: 'Halaman unggah dokumen siap',
				openLink: 'Buka halaman unggah dokumen',
				linkHint:
					'Dokumen diunggah di halaman verifikasi registrar. Tautan ini bersifat pribadi - gunakan segera, lalu kembali ke sini dan klik "Periksa Status".',
				refresh: 'Periksa Status',
				refreshed: 'Status verifikasi diperbarui.',
				resend: 'Kirim Ulang Email Verifikasi',
				resent: 'Email verifikasi dikirim ulang.',
				errors: {
					noLink: 'Registrar tidak memberikan tautan unggah dokumen. Coba lagi sesaat lagi.'
				},
				rejectedPanel: {
					title: 'Domain ditolak oleh registrar',
					body: 'Pendaftaran {domain} ditolak atau dihapus oleh registrar.',
					reason: 'Alasan registrar: {reason}',
					refunded: 'Dana {amount} telah dikembalikan sebagai kredit akun Anda.',
					noRefundDocuments:
						'Sesuai kebijakan registrar, tidak ada pengembalian dana ketika dokumen persyaratan tidak diunggah atau ditolak.',
					noRefundOther:
						'Registrar menandai pendaftaran ini tidak dapat dikembalikan dananya. Hubungi dukungan bila Anda keberatan.'
				}
			},
			toast: {
				autoRenewOn: 'Perpanjangan otomatis diaktifkan.',
				autoRenewOff: 'Perpanjangan otomatis dinonaktifkan.',
				renewCreated: 'Invoice perpanjangan berhasil dibuat.'
			},
			errors: {
				invalidRequest: 'Permintaan tidak valid.',
				actionFailed: 'Tindakan gagal. Silakan coba lagi.',
				loadFailed: 'Gagal memuat data: {message}'
			}
		},
		// domain.status 'rejected' has no label in the base dictionary
		status: { rejected: 'Ditolak' }
	},
	en: {
		clientDomains: {
			list: {
				title: 'My Domains',
				subtitle: 'Manage the domains registered on your account.',
				searchPlaceholder: 'Search domain name…',
				statusLabel: 'Status',
				colDomain: 'Domain',
				colStatus: 'Status',
				colExpiry: 'Expiry',
				colAmount: 'Renewal Price',
				colAutoRenew: 'Auto Renew',
				manage: 'Manage',
				emptyTitle: 'No domains yet',
				emptyDesc: 'You have no registered domains on this account yet.',
				registerCta: 'Register a Domain'
			},
			detail: {
				back: 'Back to domain list',
				notFound: 'Domain not found or you do not have access.',
				overviewTitle: 'Domain Information',
				renewNow: 'Renew Now',
				renewTitle: 'Renew Domain',
				renewPeriod: 'Renewal period',
				years: '{n} year(s)',
				renewTotal: 'Estimated total',
				renewHint: 'A renewal invoice will be created and you will be redirected to pay it.',
				status: 'Status',
				registrationDate: 'Registration date',
				expiryDate: 'Expiry date',
				nextDueDate: 'Next due date',
				billingCycle: 'Billing cycle',
				recurringAmount: 'Renewal price',
				autoRenew: 'Auto renew',
				idProtection: 'ID protection'
			},
			tabs: {
				overview: 'Overview',
				nameservers: 'Nameservers',
				dns: 'DNS',
				epp: 'EPP Code',
				contact: 'Contact',
				addons: 'Add-ons'
			},
			ns: {
				title: 'Nameservers',
				desc: 'Configure the nameservers for your domain. At least 2 nameservers are required.',
				label: 'Nameserver {n}',
				optionalHint: 'Optional',
				save: 'Save Nameservers',
				needTwo: 'At least 2 nameservers are required.',
				invalidHost: 'Invalid nameserver format (example: ns1.example.com).',
				saved: 'Nameservers saved successfully.'
			},
			dns: {
				title: 'DNS Manager',
				desc: 'Add, edit, or delete DNS records below, then save all changes at once.',
				type: 'Type',
				host: 'Host',
				value: 'Value',
				ttl: 'TTL',
				prio: 'Priority',
				add: 'Add Record',
				remove: 'Delete',
				save: 'Save Changes',
				empty: 'No DNS records yet. Add your first record.',
				unsaved: 'You have unsaved changes.',
				invalid: 'Invalid DNS record data.',
				invalidRow: 'Record on row {row} is invalid. Check type, host, value, TTL, and priority.',
				saved: 'DNS records saved successfully.'
			},
			epp: {
				title: 'EPP / Auth Code',
				desc: 'The EPP code is required to transfer your domain to another registrar. Keep it secret.',
				reveal: 'Reveal EPP Code',
				codeLabel: 'EPP code',
				copy: 'Copy',
				copied: 'EPP code copied to clipboard.',
				unavailable: 'The EPP code is not available right now.'
			},
			contact: {
				title: 'Registrant Contact',
				desc: 'The registrant (owner) details registered for this domain.',
				loadFailed: 'Contact data could not be loaded ({message}). You can still submit an update.',
				firstName: 'First name',
				lastName: 'Last name',
				company: 'Company',
				email: 'Email',
				phone: 'Phone',
				address: 'Address',
				city: 'City',
				state: 'State/Province',
				postcode: 'Postcode',
				country: 'Country (ISO code)',
				required: 'This field is required.',
				invalidEmail: 'Invalid email format.',
				invalidCountry: 'Use a 2-letter country code, e.g. ID.',
				save: 'Save Contact',
				saved: 'Registrant contact saved successfully.'
			},
			addons: {
				title: 'Domain Add-ons',
				desc: 'Enable add-ons for this domain. The annual renewal cost is adjusted automatically.',
				save: 'Save Add-ons',
				saved: 'Domain add-ons saved successfully.',
				perYear: '/yr',
				dnsRequiredTitle: 'DNS Management add-on required',
				dnsRequiredDesc:
					'Enable the DNS Management add-on on the Add-ons tab to manage this domain’s DNS records.',
				goToAddons: 'Go to the Add-ons tab'
			},
			cycle: {
				one_time: 'One time',
				monthly: 'Monthly',
				quarterly: 'Quarterly',
				semiannually: 'Semi-annually',
				annually: 'Annually',
				biennially: 'Biennially'
			},
			docs: {
				badge: 'Documents needed',
				badgeEmail: 'Verification needed',
				title: 'Supporting documents required',
				titleEmail: 'Email verification required',
				intro:
					'The {tld} extension requires supporting documents. This domain is active for now; upload the documents so it is not suspended.',
				introEmail:
					'The registrant must confirm the domain through the verification email the registrar sent. The domain is suspended if it is not confirmed in time.',
				stateLabel: 'Verification status',
				state: {
					waiting: 'Waiting for documents',
					verifying: 'Being verified',
					document_validating: 'Documents under review',
					active: 'Verified'
				},
				emailState: {
					waiting: 'Waiting for email verification',
					verifying: 'Being verified'
				},
				rejectedTitle: 'Documents rejected',
				rejectedWithReason:
					'The documents you uploaded were rejected: {reason}. Upload the correct documents again.',
				rejected: 'The documents you uploaded were rejected. Upload the correct documents again.',
				suspendedTitle: 'Domain suspended',
				suspendedWithReason:
					'The domain was suspended: {reason}. Complete the verification to reactivate it.',
				suspended: 'The domain was suspended. Complete the verification to reactivate it.',
				deadlinesTitle: 'Verification timeline',
				uploadBy: 'Upload documents by',
				suspendOn: 'Suspended if there are no valid documents',
				suspendOnEmail: 'Suspended if the email is not verified',
				deleteOn: 'Deleted with no refund',
				requirementsTitle: 'Documents required',
				req: {
					identity: 'Electronic ID card (KTP) / passport / e.id / other valid identity',
					companyLegal:
						'Indonesian legal entity: NIB / NPWP / Kumham decree (in the company name, e.g. PT, CV, Firma)',
					intlBrand:
						'International brand: international trademark certificate / PSE certificate (Indonesia) and a power of attorney to an Indonesian legal subject',
					orgDeed:
						'Organisation / association / foundation: notarial deed of establishment or a domain request letter from its chair',
					highEdDecree:
						'Higher-education establishment decree from the competent ministry / institution',
					requestLetterRector: 'Domain request letter from the rector or head of the institution',
					requestLetterHead:
						'Domain request letter from the school principal / head of the institution',
					schoolPermit:
						'School operating permit (or the institution establishment decree for non-formal education)',
					requestLetterPesantren:
						'Domain request letter from the head of the pesantren / institution',
					nameMatchesInstitution:
						'The domain name must match the name of the registering school / institution'
				},
				upload: 'Upload Documents',
				reupload: 'Re-upload Documents',
				linkReadyTitle: 'Your document upload page is ready',
				openLink: 'Open the document upload page',
				linkHint:
					'Documents are uploaded on the registrar verification page. The link is private - use it right away, then come back here and click "Check Status".',
				refresh: 'Check Status',
				refreshed: 'Verification status updated.',
				resend: 'Resend Verification Email',
				resent: 'Verification email re-sent.',
				errors: {
					noLink: 'The registrar did not return a document upload link. Please try again shortly.'
				},
				rejectedPanel: {
					title: 'Domain rejected by the registrar',
					body: 'The registration of {domain} was rejected or deleted by the registrar.',
					reason: 'Registrar reason: {reason}',
					refunded: '{amount} has been returned to your account credit.',
					noRefundDocuments:
						'Under the registrar policy there is no refund when the required documents were not uploaded or were rejected.',
					noRefundOther:
						'The registrar marked this registration as non-refundable. Contact support if you disagree.'
				}
			},
			toast: {
				autoRenewOn: 'Auto renew enabled.',
				autoRenewOff: 'Auto renew disabled.',
				renewCreated: 'Renewal invoice created successfully.'
			},
			errors: {
				invalidRequest: 'Invalid request.',
				actionFailed: 'The action failed. Please try again.',
				loadFailed: 'Failed to load data: {message}'
			}
		},
		status: { rejected: 'Rejected' }
	}
};

export default messages;
