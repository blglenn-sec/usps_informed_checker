# USPS Informed Delivery Email Processor

This Go project automates processing of USPS Informed Delivery emails using:

- **Gmail API** to read incoming messages
- **AWS Textract** to perform OCR on attached images
- A configurable list of target names to determine whether to keep or delete messages

If any configured target name is detected in the scanned images, the message is labeled and removed from the inbox. Otherwise, it is moved to the trash. Currently used mixing First, Last, and Maiden names.

This project was created after I moved abroad and forwarded my mail to my parents, so wanted to keep only related informed emails.

---

## Requirements

### 1. Gmail API Credentials

- Create a project in [Google Cloud Console](https://console.cloud.google.com/)
- Enable the **Gmail API**
- Create OAuth 2.0 credentials and download the `credentials.json` file
- Place `credentials.json` in the project root
- On first run, the script will open an authorization URL; paste the code back to save `token.json`

### 2. AWS Textract Credentials

- Create an IAM user with **Amazon Textract Full Access**
- Configure credentials locally using any supported method (environment variables, AWS CLI config file, or instance role)
- Example environment variables:

```bash
export AWS_ACCESS_KEY_ID="YOUR_AWS_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="YOUR_AWS_SECRET_KEY"
export AWS_REGION="us-east-1" # or your preferred region
```

### 3. Environment Variables

You must configure target names to search for in USPS image text. You can do this in either of two ways:

**Option 1 — Comma-separated list:**

```bash
export TARGET_NAMES="Paul,Newman,Joanne,Woodward"
```

**Option 2 — JSON array:**

```bash
export TARGET_NAMES_JSON='["Paul","Newman","Joanne","Woodward"]'
```

Optional overrides:

```bash
# Sender email to search for (default: USPS Informeddelivery) if you want a different purpose
export SENDER_ADDRESS="USPSInformeddelivery@email.informeddelivery.usps.com"

# Gmail label name to use for matching emails (default: USPS)
export USPS_LABEL="USPS"
```

---

## Installation & Build

1. Clone this repository:

```bash
git clone https://github.com/YOUR_GITHUB_USERNAME/your-repo.git
cd your-repo
```

2. Install dependencies:

```bash
go mod tidy
```

3. Build the binary:

```bash
go build -o usps-processor main.go
```

---

## Running

Once credentials and environment variables are set:

```bash
./usps-processor
```

On first run, you will be prompted to authenticate with Google and grant Gmail access.

---

## How It Works

1. Script searches Gmail for USPS Informed Delivery emails from the last 2 days without the specified label.
2. For each email, extracts attached images.
3. Sends each image to AWS Textract for OCR.
4. If any target name is found, adds the Gmail label and removes from inbox.
5. If no names are found, moves the email to the Trash.

---

## Example `.env.example`

```env
AWS_ACCESS_KEY_ID=your_aws_access_key
AWS_SECRET_ACCESS_KEY=your_aws_secret_key
AWS_REGION=us-east-1

TARGET_NAMES=Paul,Newman,Joanne,Woodward
# or:
# TARGET_NAMES_JSON=["Paul","Newman","Joanne","Woodward"]

SENDER_ADDRESS=USPSInformeddelivery@email.informeddelivery.usps.com
USPS_LABEL=USPS
```

---

## Notes

- Gmail API quota limits apply.
- AWS Textract incurs per-page processing charges after the 3 month free tier; check your AWS pricing but this should be as cheap as 1 dollar a month.
- The process is case-insensitive when matching target names.
- Supports both attached images and inline images (basic attachment support implemented; inline HTML parsing can be extended).

