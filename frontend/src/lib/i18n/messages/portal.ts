/**
 * PORTAL namespace - Twenty-One client/public shell + the portal pages built on
 * it (header, primary nav, breadcrumb, footer, hero, store sidebar, tiles). This
 * is an ADDITIVE feature dictionary auto-merged by i18n.svelte.ts; it never edits
 * the shared id.ts / en.ts.
 */
import type { Locale } from '../i18n.svelte';

const messages: Partial<Record<Locale, Record<string, unknown>>> = {
	id: {
		portal: {
			captcha: {
				jsRequired: 'JavaScript diperlukan untuk menyelesaikan verifikasi CAPTCHA.',
				loading: 'Memuat verifikasi keamanan…',
				failed:
					'Verifikasi keamanan gagal dimuat. Periksa koneksi internet atau ad-blocker Anda, lalu coba lagi.',
				slow: 'Verifikasi keamanan belum selesai. Tunggu sebentar atau muat ulang verifikasi.',
				retry: 'Muat ulang verifikasi'
			},
			nav: {
				home: 'Beranda',
				store: 'Toko',
				browseAll: 'Lihat Semua Produk',
				registerDomain: 'Daftar Domain Baru',
				transferDomain: 'Transfer Domain',
				announcements: 'Pengumuman',
				knowledgebase: 'Basis Pengetahuan',
				networkStatus: 'Status Jaringan',
				contactUs: 'Hubungi Kami',
				account: 'Akun',
				login: 'Masuk',
				register: 'Daftar',
				forgotPassword: 'Lupa Kata Sandi?',
				dashboard: 'Dasbor',
				myServices: 'Layanan Saya',
				myDomains: 'Domain Saya',
				myInvoices: 'Tagihan Saya',
				support: 'Dukungan',
				editAccount: 'Ubah Detail Akun',
				emailHistory: 'Riwayat Email',
				logout: 'Keluar',
				more: 'Lainnya',
				menu: 'Menu',
				close: 'Tutup'
			},
			header: {
				searchPlaceholder: 'Cari di basis pengetahuan kami...',
				search: 'Cari',
				cart: 'Keranjang Belanja'
			},
			footer: {
				poweredBy: 'Didukung oleh',
				language: 'Bahasa',
				chooseLanguage: 'Pilih Bahasa & Mata Uang',
				close: 'Tutup'
			},
			breadcrumb: {
				portalHome: 'Beranda Portal'
			},
			hero: {
				title: 'Amankan nama domain Anda',
				placeholder:
					'Cari berdasarkan kata kunci, deskripsi, atau nama domain.\nContoh: "Acara bisnis seru", "Layanan perencanaan keuangan komprehensif", atau "namabisnis.com".',
				search: 'Cari',
				transfer: 'Transfer',
				viewAllPricing: 'Lihat semua harga',
				includeTlds: 'Sertakan TLD',
				maxLength: 'Panjang Maksimum',
				safeSearch: 'Pencarian Aman'
			},
			store: {
				categories: 'Kategori',
				actions: 'Tindakan'
			},
			cart: {
				orderSummary: 'Ringkasan Pesanan',
				subtotal: 'Subtotal',
				totalDueToday: 'Total Dibayar Hari Ini',
				checkout: 'Checkout',
				continueShopping: 'Lanjut Belanja'
			},
			tiles: {
				announcements: 'Pengumuman',
				networkStatus: 'Status Jaringan',
				knowledgebase: 'Basis Pengetahuan',
				downloads: 'Unduhan',
				submitTicket: 'Kirim Tiket',
				yourAccount: 'Akun Anda',
				manageServices: 'Kelola Layanan',
				manageDomains: 'Kelola Domain',
				supportRequests: 'Permintaan Dukungan',
				makePayment: 'Lakukan Pembayaran'
			},
			help: {
				title: 'Bagaimana kami dapat membantu hari ini',
				yourAccount: 'Akun Anda'
			},
			account: {
				title: 'Akun Anda',
				profile: 'Profil',
				security: 'Keamanan'
			},
			misc: {
				monthly: 'Bulanan'
			},
			auth: {
				loginSubtitle: 'Masuk ke akun Anda untuk melanjutkan.',
				rememberMe: 'Ingat Saya',
				showPassword: 'Tampilkan kata sandi',
				hidePassword: 'Sembunyikan kata sandi'
			},
			home: {
				browseTitle: 'Jelajahi Produk & Layanan Kami',
				browseProducts: 'Lihat Produk',
				registerName: 'Daftar Domain Baru',
				registerDesc: 'Amankan nama domain Anda dengan mendaftarkannya hari ini',
				registerCta: 'Cari Domain',
				transferName: 'Transfer Domain Anda',
				transferDesc: 'Transfer sekarang untuk memperpanjang domain Anda 1 tahun',
				transferCta: 'Transfer Domain Anda'
			},
			announcements: {
				title: 'Pengumuman',
				subtitle: 'Berita dan pembaruan terbaru dari kami.',
				empty: 'Belum ada pengumuman.',
				readMore: 'Selengkapnya',
				notFound: 'Pengumuman tidak ditemukan.',
				back: 'Kembali ke pengumuman',
				prev: 'Sebelumnya',
				next: 'Berikutnya'
			},
			kb: {
				title: 'Basis Pengetahuan',
				subtitle: 'Telusuri artikel bantuan berdasarkan kategori atau cari di bawah.',
				searchPlaceholder: 'Cari di basis pengetahuan...',
				search: 'Cari',
				resultsTitle: 'Hasil pencarian untuk "{term}"',
				noResults: 'Tidak ada artikel yang cocok dengan pencarian Anda.',
				noCategories: 'Belum ada kategori.',
				views: 'kali dilihat',
				notFoundCategory: 'Kategori tidak ditemukan.',
				notFoundArticle: 'Artikel tidak ditemukan.',
				back: 'Kembali ke Basis Pengetahuan',
				emptyCategory: 'Belum ada artikel di kategori ini.'
			},
			networkStatus: {
				title: 'Status Jaringan',
				subtitle: 'Insiden dan pemeliharaan terjadwal saat ini.',
				allOperational: 'Semua sistem beroperasi normal.',
				colTitle: 'Judul',
				colType: 'Tipe',
				colSeverity: 'Tingkat',
				colStatus: 'Status',
				colAffected: 'Terdampak',
				colStarted: 'Dimulai',
				type: {
					scheduled: 'Terjadwal',
					issue: 'Gangguan',
					outage: 'Pemadaman'
				},
				severity: {
					minor: 'Ringan',
					major: 'Berat',
					critical: 'Kritis'
				},
				statusLabel: {
					investigating: 'Menyelidiki',
					identified: 'Teridentifikasi',
					monitoring: 'Memantau',
					resolved: 'Selesai',
					scheduled: 'Terjadwal'
				}
			},
			contact: {
				title: 'Hubungi Kami',
				subtitle: 'Ada pertanyaan? Kirimkan pesan kepada kami dan tim kami akan segera merespons.',
				name: 'Nama',
				email: 'Alamat Email',
				subject: 'Subjek',
				message: 'Pesan',
				department: 'Departemen',
				departmentPlaceholder: 'Pilih departemen (opsional)',
				submit: 'Kirim Pesan',
				success: 'Terima kasih! Pesan Anda telah kami terima. Nomor tiket Anda adalah {number}.',
				error: 'Terjadi kesalahan. Silakan coba lagi.',
				fillAllFields: 'Mohon lengkapi semua kolom yang wajib diisi.'
			}
		}
	},
	en: {
		portal: {
			captcha: {
				jsRequired: 'JavaScript is required to complete the CAPTCHA.',
				loading: 'Loading security check…',
				failed:
					'The security check failed to load. Check your internet connection or ad-blocker and try again.',
				slow: 'The security check has not finished yet. Please wait a moment or reload it.',
				retry: 'Reload security check'
			},
			nav: {
				home: 'Home',
				store: 'Store',
				browseAll: 'Browse All',
				registerDomain: 'Register a Domain',
				transferDomain: 'Transfer a Domain',
				announcements: 'Announcements',
				knowledgebase: 'Knowledgebase',
				networkStatus: 'Network Status',
				contactUs: 'Contact Us',
				account: 'Account',
				login: 'Login',
				register: 'Register',
				forgotPassword: 'Forgot Password?',
				dashboard: 'Dashboard',
				myServices: 'My Services',
				myDomains: 'My Domains',
				myInvoices: 'My Invoices',
				support: 'Support',
				editAccount: 'Edit Account Details',
				emailHistory: 'Email History',
				logout: 'Logout',
				more: 'More',
				menu: 'Menu',
				close: 'Close'
			},
			header: {
				searchPlaceholder: 'Search our knowledgebase...',
				search: 'Search',
				cart: 'Shopping Cart'
			},
			footer: {
				poweredBy: 'Powered by',
				language: 'Language',
				chooseLanguage: 'Choose Language & Currency',
				close: 'Close'
			},
			breadcrumb: {
				portalHome: 'Portal Home'
			},
			hero: {
				title: 'Secure your domain name',
				placeholder:
					'Search by keyword, description, or domain.\nFor example: "Fun business events", "A comprehensive financial planning service for professionals", or "example.com".',
				search: 'Search',
				transfer: 'Transfer',
				viewAllPricing: 'View all pricing',
				includeTlds: 'Include TLDs',
				maxLength: 'Maximum Length',
				safeSearch: 'Safe Search'
			},
			store: {
				categories: 'Categories',
				actions: 'Actions'
			},
			cart: {
				orderSummary: 'Order Summary',
				subtotal: 'Subtotal',
				totalDueToday: 'Total Due Today',
				checkout: 'Checkout',
				continueShopping: 'Continue Shopping'
			},
			tiles: {
				announcements: 'Announcements',
				networkStatus: 'Network Status',
				knowledgebase: 'Knowledgebase',
				downloads: 'Downloads',
				submitTicket: 'Submit a Ticket',
				yourAccount: 'Your Account',
				manageServices: 'Manage Services',
				manageDomains: 'Manage Domains',
				supportRequests: 'Support Requests',
				makePayment: 'Make a Payment'
			},
			help: {
				title: 'How can we help today',
				yourAccount: 'Your Account'
			},
			account: {
				title: 'Your Account',
				profile: 'Profile',
				security: 'Security'
			},
			misc: {
				monthly: 'Monthly'
			},
			auth: {
				loginSubtitle: 'Sign in to your account to continue.',
				rememberMe: 'Remember Me',
				showPassword: 'Show password',
				hidePassword: 'Hide password'
			},
			home: {
				browseTitle: 'Browse our Products & Services',
				browseProducts: 'Browse Products',
				registerName: 'Register a New Domain',
				registerDesc: 'Secure your domain name by registering it today',
				registerCta: 'Domain Search',
				transferName: 'Transfer Your Domain',
				transferDesc: 'Transfer now to extend your domain by 1 year',
				transferCta: 'Transfer Your Domain'
			},
			announcements: {
				title: 'Announcements',
				subtitle: 'Our latest news and updates.',
				empty: 'No announcements yet.',
				readMore: 'Read more',
				notFound: 'Announcement not found.',
				back: 'Back to announcements',
				prev: 'Previous',
				next: 'Next'
			},
			kb: {
				title: 'Knowledgebase',
				subtitle: 'Browse help articles by category or search below.',
				searchPlaceholder: 'Search the knowledgebase...',
				search: 'Search',
				resultsTitle: 'Search results for "{term}"',
				noResults: 'No articles match your search.',
				noCategories: 'No categories yet.',
				views: 'views',
				notFoundCategory: 'Category not found.',
				notFoundArticle: 'Article not found.',
				back: 'Back to Knowledgebase',
				emptyCategory: 'No articles in this category yet.'
			},
			networkStatus: {
				title: 'Network Status',
				subtitle: 'Current incidents and scheduled maintenance.',
				allOperational: 'All systems operational.',
				colTitle: 'Title',
				colType: 'Type',
				colSeverity: 'Severity',
				colStatus: 'Status',
				colAffected: 'Affected',
				colStarted: 'Started',
				type: {
					scheduled: 'Scheduled',
					issue: 'Issue',
					outage: 'Outage'
				},
				severity: {
					minor: 'Minor',
					major: 'Major',
					critical: 'Critical'
				},
				statusLabel: {
					investigating: 'Investigating',
					identified: 'Identified',
					monitoring: 'Monitoring',
					resolved: 'Resolved',
					scheduled: 'Scheduled'
				}
			},
			contact: {
				title: 'Contact Us',
				subtitle: 'Have a question? Send us a message and our team will get back to you shortly.',
				name: 'Name',
				email: 'Email Address',
				subject: 'Subject',
				message: 'Message',
				department: 'Department',
				departmentPlaceholder: 'Select a department (optional)',
				submit: 'Send Message',
				success: 'Thanks! Your message has been received. Your ticket number is {number}.',
				error: 'Something went wrong. Please try again.',
				fillAllFields: 'Please fill in all required fields.'
			}
		}
	}
};

export default messages;
