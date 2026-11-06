/**
 * FE-CLIENT-SNIPES i18n namespace - keys under `portal.snipe.*` plus the two
 * nav labels under `portal.nav.*`. Deep-merged over portal.ts by
 * i18n.svelte.ts (the shared id.ts / en.ts are never edited).
 */
import type { Locale } from '../i18n.svelte';

const messages: Partial<Record<Locale, Record<string, unknown>>> = {
	id: {
		portal: {
			nav: {
				mySnipes: 'Perburuan Domain',
				domainSnipe: 'Buru Domain (Snipe)'
			},
			snipe: {
				status: {
					pending_payment: 'Menunggu pembayaran',
					active: 'Sedang berburu',
					won: 'Berhasil didapat',
					expired: 'Berakhir',
					cancelled: 'Dibatalkan',
					failed: 'Dihentikan'
				},
				phase: {
					patient: 'Memantau ketersediaan',
					burst: 'Mencoba mendaftar'
				},
				list: {
					title: 'Perburuan Domain',
					subtitle:
						'Domain yang sedang kami buru untuk Anda. Begitu domain expired dilepas registry, kami mendaftarkannya secepat mungkin.',
					newCta: 'Mulai Perburuan',
					searchPlaceholder: 'Cari nama domain…',
					statusLabel: 'Status',
					colDomain: 'Domain',
					colStatus: 'Status',
					colPlan: 'Paket',
					colDeadline: 'Berburu hingga',
					colAttempts: 'Percobaan',
					view: 'Lihat',
					emptyTitle: 'Belum ada perburuan',
					emptyDesc:
						'Ingin domain yang sedang expired atau akan dilepas? Mulai perburuan dan kami yang mengurusnya.',
					loadFailed: 'Gagal memuat daftar perburuan: {message}'
				},
				new: {
					title: 'Mulai Perburuan Domain',
					subtitle:
						'Masukkan domain yang sedang expired atau akan dilepas. Kami memantaunya dan mendaftarkannya otomatis begitu tersedia.',
					domainLabel: 'Nama domain',
					domainPlaceholder: 'contoh.com',
					domainHint:
						'Domain yang sudah bisa didaftarkan sekarang tidak perlu diburu: daftarkan langsung di toko.',
					planLabel: 'Pilih paket',
					planDetail: 'Berburu hingga {days} hari · biaya layanan {fee}',
					planFree: 'Gratis biaya layanan',
					planSuccess: ' · biaya sukses {fee}',
					noPlans: 'Belum ada paket perburuan yang tersedia. Silakan hubungi kami.',
					yearsLabel: 'Lama pendaftaran jika berhasil',
					years: '{n} tahun',
					windowToggle: 'Saya tahu perkiraan waktu domain dilepas',
					windowHint:
						'Opsional. Kami baru menembak pendaftaran intensif di jendela waktu ini (mulai sedikit lebih awal). Di luar itu kami hanya memantau. Isi waktu mulai dan selesai; panjang jendela maksimal 6 jam. Waktu dalam WIB.',
					windowFrom: 'Mulai',
					windowUntil: 'Selesai',
					nsToggle: 'Atur nameserver sendiri',
					nsHint: 'Opsional. Kosongkan untuk memakai nameserver bawaan.',
					nsLabel: 'Nameserver {n}',
					quoteTitle: 'Rincian biaya',
					quoteFee: 'Biaya layanan perburuan (tidak dikembalikan)',
					quoteDomain: 'Harga registrasi domain · {n} tahun',
					quoteSuccess: 'Biaya sukses',
					quoteTax: 'Pajak ({rate}%)',
					quoteTaxIncluded: 'Termasuk pajak ({rate}%)',
					quoteTotal: 'Total dibayar sekarang',
					quoteRefundable: 'Dikembalikan ke saldo credit jika domain tidak didapat',
					quoteLoading: 'Menghitung…',
					quoteNeedInput: 'Isi domain dan pilih paket untuk melihat rincian biaya.',
					quoteFailed: 'Tidak bisa menghitung biaya: {message}',
					terms:
						'Anda membayar di muka. Jika domain berhasil didapat, domain langsung terdaftar atas nama Anda. Jika tidak berhasil sampai batas waktu, harga domain dikembalikan ke saldo credit akun Anda; biaya layanan tidak dikembalikan.',
					submit: 'Lanjut ke Pembayaran',
					submitting: 'Memproses…',
					createFailed: 'Perburuan belum bisa dibuat'
				},
				detail: {
					back: 'Kembali ke daftar perburuan',
					overview: 'Ringkasan',
					plan: 'Paket',
					years: 'Lama pendaftaran',
					window: 'Jendela waktu drop',
					windowNone: 'Tidak diatur (dipantau terus)',
					deadline: 'Berburu hingga',
					attempts: 'Percobaan',
					phase: 'Aktivitas',
					created: 'Dibuat',
					paidAt: 'Dibayar',
					wonAt: 'Berhasil pada',
					billing: 'Pembayaran',
					invoice: 'Tagihan',
					fee: 'Biaya layanan',
					domainAmount: 'Harga domain (dimuka)',
					refundable: 'Dikembalikan jika tidak didapat',
					refunded: 'Sudah dikembalikan',
					payNow: 'Bayar Sekarang',
					payHint: 'Perburuan dimulai otomatis begitu pembayaran diterima.',
					viewDomain: 'Kelola Domain',
					cancel: 'Batalkan Perburuan',
					cancelTitle: 'Batalkan perburuan?',
					cancelBodyUnpaid: 'Perburuan belum dibayar dan akan dibatalkan beserta tagihannya.',
					cancelBodyPaid:
						'Harga domain ({amount}) dikembalikan ke saldo credit Anda. Biaya layanan tidak dikembalikan.',
					cancelConfirm: 'Ya, batalkan',
					keep: 'Lanjutkan berburu',
					timeline: 'Riwayat',
					timelineEmpty: 'Belum ada riwayat.',
					notFound: 'Perburuan tidak ditemukan.',
					events: {
						created: 'Perburuan dibuat, menunggu pembayaran',
						pending_payment: 'Menunggu pembayaran',
						paid: 'Pembayaran diterima, perburuan dimulai',
						available: 'Domain terlihat tersedia, mulai mendaftar',
						won: 'Domain berhasil didaftarkan',
						expired: 'Batas waktu berburu habis',
						cancelled: 'Perburuan dibatalkan',
						failed: 'Perburuan dihentikan'
					}
				},
				toast: {
					cancelled: 'Perburuan dibatalkan',
					cancelFailed: 'Perburuan belum bisa dibatalkan'
				}
			}
		}
	},
	en: {
		portal: {
			nav: {
				mySnipes: 'Domain Hunts',
				domainSnipe: 'Hunt a Domain (Snipe)'
			},
			snipe: {
				status: {
					pending_payment: 'Awaiting payment',
					active: 'Hunting',
					won: 'Secured',
					expired: 'Ended',
					cancelled: 'Cancelled',
					failed: 'Stopped'
				},
				phase: {
					patient: 'Watching availability',
					burst: 'Trying to register'
				},
				list: {
					title: 'Domain Hunts',
					subtitle:
						'Domains we are hunting for you. As soon as the registry releases an expired domain we register it as fast as we can.',
					newCta: 'Start a Hunt',
					searchPlaceholder: 'Search domain name…',
					statusLabel: 'Status',
					colDomain: 'Domain',
					colStatus: 'Status',
					colPlan: 'Plan',
					colDeadline: 'Hunting until',
					colAttempts: 'Attempts',
					view: 'View',
					emptyTitle: 'No hunts yet',
					emptyDesc:
						'Want a domain that is expiring or about to drop? Start a hunt and we take care of it.',
					loadFailed: 'Could not load your hunts: {message}'
				},
				new: {
					title: 'Start a Domain Hunt',
					subtitle:
						'Enter a domain that is expired or about to drop. We watch it and register it automatically the moment it becomes available.',
					domainLabel: 'Domain name',
					domainPlaceholder: 'example.com',
					domainHint:
						'A domain you can register right now does not need a hunt: order it in the store.',
					planLabel: 'Choose a plan',
					planDetail: 'Hunts up to {days} days · service fee {fee}',
					planFree: 'No service fee',
					planSuccess: ' · success fee {fee}',
					noPlans: 'No hunting plans are available yet. Please contact us.',
					yearsLabel: 'Registration length if we win',
					years: '{n} year(s)',
					windowToggle: 'I know roughly when the domain drops',
					windowHint:
						'Optional. We only fire intensive registration attempts inside this window (starting a little early); outside it we just watch. Fill in both a start and an end; the window can span at most 6 hours. Times are WIB.',
					windowFrom: 'From',
					windowUntil: 'Until',
					nsToggle: 'Set my own nameservers',
					nsHint: 'Optional. Leave empty to use the default nameservers.',
					nsLabel: 'Nameserver {n}',
					quoteTitle: 'Price breakdown',
					quoteFee: 'Hunting service fee (not refunded)',
					quoteDomain: 'Domain registration price · {n} year(s)',
					quoteSuccess: 'Success fee',
					quoteTax: 'Tax ({rate}%)',
					quoteTaxIncluded: 'Includes tax ({rate}%)',
					quoteTotal: 'Total due now',
					quoteRefundable: 'Credited back to your account if the domain is not secured',
					quoteLoading: 'Calculating…',
					quoteNeedInput: 'Enter a domain and pick a plan to see the price.',
					quoteFailed: 'Could not calculate the price: {message}',
					terms:
						'You pay up front. If we secure the domain it is registered in your name right away. If we do not get it before the deadline, the domain price is credited back to your account balance; the service fee is not refunded.',
					submit: 'Continue to Payment',
					submitting: 'Processing…',
					createFailed: 'The hunt could not be created'
				},
				detail: {
					back: 'Back to my hunts',
					overview: 'Overview',
					plan: 'Plan',
					years: 'Registration length',
					window: 'Drop window',
					windowNone: 'Not set (watched continuously)',
					deadline: 'Hunting until',
					attempts: 'Attempts',
					phase: 'Activity',
					created: 'Created',
					paidAt: 'Paid',
					wonAt: 'Secured on',
					billing: 'Payment',
					invoice: 'Invoice',
					fee: 'Service fee',
					domainAmount: 'Domain price (paid up front)',
					refundable: 'Credited back if not secured',
					refunded: 'Already credited back',
					payNow: 'Pay Now',
					payHint: 'The hunt starts automatically as soon as the payment is received.',
					viewDomain: 'Manage Domain',
					cancel: 'Cancel Hunt',
					cancelTitle: 'Cancel this hunt?',
					cancelBodyUnpaid: 'The hunt is unpaid and will be cancelled together with its invoice.',
					cancelBodyPaid:
						'The domain price ({amount}) is credited back to your account balance. The service fee is not refunded.',
					cancelConfirm: 'Yes, cancel',
					keep: 'Keep hunting',
					timeline: 'History',
					timelineEmpty: 'No history yet.',
					notFound: 'Hunt not found.',
					events: {
						created: 'Hunt created, awaiting payment',
						pending_payment: 'Awaiting payment',
						paid: 'Payment received, the hunt started',
						available: 'The domain looked available, started registering',
						won: 'The domain was registered',
						expired: 'The hunting window ended',
						cancelled: 'The hunt was cancelled',
						failed: 'The hunt was stopped'
					}
				},
				toast: {
					cancelled: 'Hunt cancelled',
					cancelFailed: 'The hunt could not be cancelled'
				}
			}
		}
	}
};

export default messages;
