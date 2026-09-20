/**
 * FE-AUTH namespace - register / verify-email / forgot-password / reset-password.
 * Deep-merged over the base dictionaries (see $lib/i18n/i18n.svelte.ts).
 */
const messages = {
	id: {
		feauth: {
			validation: {
				required: 'Wajib diisi.',
				invalidEmail: 'Alamat email tidak valid.',
				passwordMin: 'Kata sandi minimal 8 karakter.',
				passwordMismatch: 'Konfirmasi kata sandi tidak cocok.'
			},
			register: {
				title: 'Buat akun baru',
				subtitle: 'Daftar untuk mulai memesan layanan hosting dan domain.',
				firstName: 'Nama depan',
				lastName: 'Nama belakang',
				company: 'Perusahaan',
				address: 'Alamat',
				city: 'Kota',
				state: 'Provinsi',
				postcode: 'Kode pos',
				country: 'Negara',
				phone: 'Nomor telepon',
				passwordHint: 'Minimal 8 karakter.',
				emailTaken: 'Email ini sudah terdaftar. Silakan masuk atau gunakan email lain.',
				failed: 'Pendaftaran gagal. Silakan coba lagi.',
				successTitle: 'Periksa email Anda',
				successBody:
					'Kami telah mengirim tautan verifikasi ke {email}. Klik tautan tersebut untuk mengaktifkan akun Anda sebelum masuk.',
				resend: 'Kirim ulang email verifikasi',
				resendSuccess: 'Email verifikasi telah dikirim ulang.',
				resendFailed: 'Gagal mengirim ulang email verifikasi. Silakan coba lagi.'
			},
			verify: {
				verifying: 'Memverifikasi email Anda…',
				successTitle: 'Email terverifikasi',
				successBody: 'Email Anda berhasil diverifikasi. Silakan masuk untuk melanjutkan.',
				goLogin: 'Masuk sekarang',
				failedTitle: 'Verifikasi gagal',
				missingToken: 'Tautan verifikasi tidak valid: token tidak ditemukan.',
				failedExpired: 'Token verifikasi tidak valid atau sudah kedaluwarsa.',
				resendPrompt: 'Butuh tautan baru? Masukkan alamat email Anda dan kami akan mengirim ulang.',
				resendSubmit: 'Kirim ulang'
			},
			forgot: {
				subtitle:
					'Masukkan alamat email akun Anda. Kami akan mengirim tautan untuk mengatur ulang kata sandi.',
				submit: 'Kirim tautan reset',
				successTitle: 'Periksa email Anda',
				successBody:
					'Jika {email} terdaftar, kami telah mengirim tautan reset kata sandi. Periksa kotak masuk dan folder spam Anda.',
				backToLogin: 'Kembali ke halaman masuk'
			},
			reset: {
				subtitle: 'Buat kata sandi baru untuk akun Anda.',
				newPassword: 'Kata sandi baru',
				submit: 'Simpan kata sandi baru',
				success: 'Kata sandi berhasil diubah. Silakan masuk dengan kata sandi baru Anda.',
				invalidTitle: 'Tautan tidak valid',
				missingToken: 'Tautan reset tidak valid: token tidak ditemukan.',
				failedExpired: 'Token reset tidak valid atau sudah kedaluwarsa. Silakan minta tautan baru.',
				requestNew: 'Minta tautan baru'
			}
		}
	},
	en: {
		feauth: {
			validation: {
				required: 'This field is required.',
				invalidEmail: 'Invalid email address.',
				passwordMin: 'Password must be at least 8 characters.',
				passwordMismatch: 'Password confirmation does not match.'
			},
			register: {
				title: 'Create a new account',
				subtitle: 'Sign up to start ordering hosting services and domains.',
				firstName: 'First name',
				lastName: 'Last name',
				company: 'Company',
				address: 'Address',
				city: 'City',
				state: 'State/Province',
				postcode: 'Postcode',
				country: 'Country',
				phone: 'Phone number',
				passwordHint: 'At least 8 characters.',
				emailTaken: 'This email is already registered. Please log in or use another email.',
				failed: 'Registration failed. Please try again.',
				successTitle: 'Check your email',
				successBody:
					'We have sent a verification link to {email}. Click the link to activate your account before logging in.',
				resend: 'Resend verification email',
				resendSuccess: 'Verification email has been resent.',
				resendFailed: 'Failed to resend the verification email. Please try again.'
			},
			verify: {
				verifying: 'Verifying your email…',
				successTitle: 'Email verified',
				successBody: 'Your email has been verified successfully. Please log in to continue.',
				goLogin: 'Log in now',
				failedTitle: 'Verification failed',
				missingToken: 'Invalid verification link: token is missing.',
				failedExpired: 'The verification token is invalid or has expired.',
				resendPrompt: 'Need a new link? Enter your email address and we will resend it.',
				resendSubmit: 'Resend'
			},
			forgot: {
				subtitle:
					'Enter your account email address. We will send you a link to reset your password.',
				submit: 'Send reset link',
				successTitle: 'Check your email',
				successBody:
					'If {email} is registered, we have sent a password reset link. Check your inbox and spam folder.',
				backToLogin: 'Back to login'
			},
			reset: {
				subtitle: 'Create a new password for your account.',
				newPassword: 'New password',
				submit: 'Save new password',
				success: 'Password changed successfully. Please log in with your new password.',
				invalidTitle: 'Invalid link',
				missingToken: 'Invalid reset link: token is missing.',
				failedExpired: 'The reset token is invalid or has expired. Please request a new link.',
				requestNew: 'Request a new link'
			}
		}
	}
};

export default messages;
