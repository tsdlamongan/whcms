import type { Locale } from '../i18n.svelte';

const messages: Partial<Record<Locale, Record<string, unknown>>> = {
	id: {
		install: {
			title: 'Pemasangan {name}',
			infra: {
				heading: 'Hubungkan infrastruktur',
				intro:
					'Beberapa pengaturan wajib belum ditemukan. Isi koneksi di bawah ini — Anda bisa menguji tiap koneksi sebelum melanjutkan.',
				missing: 'Belum diisi: {vars}',
				app: 'Aplikasi',
				appBaseUrl: 'URL API (publik)',
				frontendUrl: 'URL Frontend (publik)',
				database: 'Basis data (PostgreSQL)',
				dbHost: 'Host',
				dbPort: 'Port',
				dbUser: 'Pengguna',
				dbPassword: 'Kata sandi',
				dbName: 'Nama basis data',
				dbSslMode: 'Mode SSL',
				redis: 'Redis',
				redisAddr: 'Alamat (host:port)',
				redisPassword: 'Kata sandi (opsional)',
				storage: 'Penyimpanan (S3 / RustFS)',
				storageEndpoint: 'Endpoint',
				storageAccessKey: 'Access key',
				storageSecretKey: 'Secret key',
				storageBucket: 'Bucket',
				storageUseSsl: 'Gunakan SSL',
				mail: 'Email',
				mailDriver: 'Driver',
				mailDriverLog: 'Log (hanya untuk pengembangan)',
				mailDriverSmtp: 'SMTP',
				mailDriverHttp: 'HTTP (webhook)',
				smtpHost: 'SMTP host',
				smtpPort: 'SMTP port',
				smtpUser: 'SMTP pengguna',
				smtpPass: 'SMTP kata sandi',
				mailHttpUrl: 'URL webhook',
				test: 'Uji koneksi',
				testing: 'Menguji…',
				testOk: 'Berhasil terhubung',
				saveAndContinue: 'Simpan & lanjutkan',
				saving: 'Menyimpan…'
			},
			restarting: {
				heading: 'Menyimpan konfigurasi…',
				body: 'Server sedang memuat ulang dengan konfigurasi baru. Halaman ini akan otomatis melanjutkan begitu server siap kembali.'
			},
			admin: {
				heading: 'Buat akun admin pertama',
				intro: 'Akun ini akan memiliki akses penuh ke panel admin {name}.',
				email: 'Email',
				password: 'Kata sandi',
				passwordHint: 'Minimal 8 karakter.',
				submit: 'Buat akun admin',
				submitting: 'Membuat akun…'
			},
			settings: {
				heading: 'Pengaturan situs (opsional)',
				intro: 'Bisa diisi sekarang atau nanti lewat Admin → Pengaturan.',
				companyName: 'Nama perusahaan',
				companyEmail: 'Email perusahaan',
				companyAddress: 'Alamat',
				submit: 'Simpan & selesai',
				submitting: 'Menyimpan…',
				skip: 'Lewati, lakukan nanti'
			},
			errors: {
				invalidBody: 'Permintaan tidak valid',
				generic: 'Terjadi kesalahan, silakan coba lagi'
			}
		}
	},
	en: {
		install: {
			title: '{name} Installation',
			infra: {
				heading: 'Connect your infrastructure',
				intro:
					"Some required settings weren't found. Fill in the connections below — you can test each one before continuing.",
				missing: 'Missing: {vars}',
				app: 'Application',
				appBaseUrl: 'API URL (public)',
				frontendUrl: 'Frontend URL (public)',
				database: 'Database (PostgreSQL)',
				dbHost: 'Host',
				dbPort: 'Port',
				dbUser: 'User',
				dbPassword: 'Password',
				dbName: 'Database name',
				dbSslMode: 'SSL mode',
				redis: 'Redis',
				redisAddr: 'Address (host:port)',
				redisPassword: 'Password (optional)',
				storage: 'Storage (S3 / RustFS)',
				storageEndpoint: 'Endpoint',
				storageAccessKey: 'Access key',
				storageSecretKey: 'Secret key',
				storageBucket: 'Bucket',
				storageUseSsl: 'Use SSL',
				mail: 'Mail',
				mailDriver: 'Driver',
				mailDriverLog: 'Log (development only)',
				mailDriverSmtp: 'SMTP',
				mailDriverHttp: 'HTTP (webhook)',
				smtpHost: 'SMTP host',
				smtpPort: 'SMTP port',
				smtpUser: 'SMTP user',
				smtpPass: 'SMTP password',
				mailHttpUrl: 'Webhook URL',
				test: 'Test connection',
				testing: 'Testing…',
				testOk: 'Connected successfully',
				saveAndContinue: 'Save & continue',
				saving: 'Saving…'
			},
			restarting: {
				heading: 'Saving configuration…',
				body: 'The server is restarting with the new configuration. This page will automatically continue once it comes back up.'
			},
			admin: {
				heading: 'Create the first admin account',
				intro: 'This account will have full access to the {name} admin panel.',
				email: 'Email',
				password: 'Password',
				passwordHint: 'At least 8 characters.',
				submit: 'Create admin account',
				submitting: 'Creating account…'
			},
			settings: {
				heading: 'Site settings (optional)',
				intro: 'You can fill these in now or later via Admin → Settings.',
				companyName: 'Company name',
				companyEmail: 'Company email',
				companyAddress: 'Address',
				submit: 'Save & finish',
				submitting: 'Saving…',
				skip: 'Skip, do this later'
			},
			errors: {
				invalidBody: 'Invalid request',
				generic: 'Something went wrong, please try again'
			}
		}
	}
};

export default messages;
